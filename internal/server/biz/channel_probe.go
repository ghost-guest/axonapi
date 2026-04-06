package biz

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	"github.com/zhenzou/executors"
	"go.uber.org/fx"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelprobe"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xtime"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/gql/qb"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// ChannelProbePoint represents a single probe data point for a channel.
type ChannelProbePoint struct {
	Timestamp             int64    `json:"timestamp"`
	TotalRequestCount     int      `json:"total_request_count"`
	SuccessRequestCount   int      `json:"success_request_count"`
	AvgTokensPerSecond    *float64 `json:"avg_tokens_per_second,omitempty"`
	AvgTimeToFirstTokenMs *float64 `json:"avg_time_to_first_token_ms,omitempty"`
}

// ChannelProbeData represents probe data for a single channel.
type ChannelProbeData struct {
	ChannelID int                  `json:"channel_id"`
	Points    []*ChannelProbePoint `json:"points"`
}

// ChannelProbeServiceParams contains dependencies for ChannelProbeService.
type ChannelProbeServiceParams struct {
	fx.In

	Ent                 *ent.Client
	ChannelService      *ChannelService
	SystemService       *SystemService
	ModelCircuitBreaker *ModelCircuitBreaker
}

// ChannelProbeService handles channel probe operations.
type ChannelProbeService struct {
	*AbstractService

	SystemService        *SystemService
	ChannelService       *ChannelService
	ModelCircuitBreaker  *ModelCircuitBreaker
	Executor             executors.ScheduledExecutor
	mu                   sync.Mutex
	lastExecutionByChan  map[int]time.Time
	randomScheduleByChan map[int]channelRandomSchedule
}

// NewChannelProbeService creates a new ChannelProbeService.
func NewChannelProbeService(params ChannelProbeServiceParams) *ChannelProbeService {
	svc := &ChannelProbeService{
		AbstractService: &AbstractService{
			db: params.Ent,
		},
		SystemService:        params.SystemService,
		ChannelService:       params.ChannelService,
		ModelCircuitBreaker:  params.ModelCircuitBreaker,
		Executor:             executors.NewPoolScheduleExecutor(executors.WithMaxConcurrent(1)),
		lastExecutionByChan:  make(map[int]time.Time),
		randomScheduleByChan: make(map[int]channelRandomSchedule),
	}

	return svc
}

// Start starts the channel probe service with scheduled task.
func (svc *ChannelProbeService) Start(ctx context.Context) error {
	_, err := svc.Executor.ScheduleFuncAtCronRate(
		svc.runProbePeriodically,
		executors.CRONRule{Expr: "* * * * *"},
	)

	return err
}

// Stop stops the channel probe service.
func (svc *ChannelProbeService) Stop(ctx context.Context) error {
	return svc.Executor.Shutdown(ctx)
}

func probeFrequencyToDuration(frequency ProbeFrequency) time.Duration {
	switch frequency {
	case ProbeFrequency1Min:
		return time.Minute
	case ProbeFrequency5Min:
		return 5 * time.Minute
	case ProbeFrequency30Min:
		return 30 * time.Minute
	case ProbeFrequency1Hour:
		return time.Hour
	default:
		return time.Minute
	}
}

func legacyChannelProbeFrequencyToDuration(frequency objects.ChannelProbeFrequency) (time.Duration, bool) {
	switch frequency {
	case objects.ChannelProbeFrequency1Min:
		return time.Minute, true
	case objects.ChannelProbeFrequency5Min:
		return 5 * time.Minute, true
	case objects.ChannelProbeFrequency30Min:
		return 30 * time.Minute, true
	case objects.ChannelProbeFrequency1Hour:
		return time.Hour, true
	default:
		return 0, false
	}
}

type channelProbePlan struct {
	enabled       bool
	mode          objects.ChannelProbeIntervalMode
	fixedInterval time.Duration
	randomMin     time.Duration
	randomMax     time.Duration
}

type channelRandomSchedule struct {
	interval        time.Duration
	nextExecutionAt time.Time
}

type dueChannelProbeWindow struct {
	channelID int
	startTime time.Time
	endTime   time.Time
	interval  time.Duration
	mode      objects.ChannelProbeIntervalMode
}

func resolveChannelProbePlan(settings *objects.ChannelSettings, fallback ProbeFrequency) channelProbePlan {
	plan := channelProbePlan{
		enabled:       true,
		mode:          objects.ChannelProbeIntervalModeFixed,
		fixedInterval: probeFrequencyToDuration(fallback),
	}

	if settings == nil {
		return plan
	}

	if settings.ProbeEnabled != nil {
		plan.enabled = *settings.ProbeEnabled
	}
	if !plan.enabled {
		return plan
	}

	switch settings.ProbeIntervalMode {
	case objects.ChannelProbeIntervalModeFixed:
		if settings.ProbeFixedIntervalSeconds > 0 {
			plan.mode = objects.ChannelProbeIntervalModeFixed
			plan.fixedInterval = time.Duration(settings.ProbeFixedIntervalSeconds) * time.Second
			return plan
		}
	case objects.ChannelProbeIntervalModeRandom:
		if settings.ProbeRandomMinIntervalSeconds > 0 && settings.ProbeRandomMaxIntervalSeconds >= settings.ProbeRandomMinIntervalSeconds {
			plan.mode = objects.ChannelProbeIntervalModeRandom
			plan.randomMin = time.Duration(settings.ProbeRandomMinIntervalSeconds) * time.Second
			plan.randomMax = time.Duration(settings.ProbeRandomMaxIntervalSeconds) * time.Second
			return plan
		}
	}

	if interval, ok := legacyChannelProbeFrequencyToDuration(settings.ProbeFrequency); ok {
		plan.mode = objects.ChannelProbeIntervalModeFixed
		plan.fixedInterval = interval
	}

	return plan
}

func sampleRandomProbeInterval(min time.Duration, max time.Duration) time.Duration {
	if max <= min {
		return min
	}

	delta := max - min
	return min + time.Duration(rand.Int64N(int64(delta)+1))
}

func (svc *ChannelProbeService) cleanupChannelProbeState(activeChannelIDs map[int]struct{}) {
	for channelID := range svc.lastExecutionByChan {
		if _, ok := activeChannelIDs[channelID]; !ok {
			delete(svc.lastExecutionByChan, channelID)
		}
	}
	for channelID := range svc.randomScheduleByChan {
		if _, ok := activeChannelIDs[channelID]; !ok {
			delete(svc.randomScheduleByChan, channelID)
		}
	}
}

func (svc *ChannelProbeService) collectDueChannelProbeWindows(
	channels []*ent.Channel,
	fallback ProbeFrequency,
	now time.Time,
) []dueChannelProbeWindow {
	activeChannelIDs := make(map[int]struct{}, len(channels))
	windows := make([]dueChannelProbeWindow, 0, len(channels))

	svc.mu.Lock()
	defer svc.mu.Unlock()

	for _, ch := range channels {
		activeChannelIDs[ch.ID] = struct{}{}
	}
	svc.cleanupChannelProbeState(activeChannelIDs)

	for _, ch := range channels {
		plan := resolveChannelProbePlan(ch.Settings, fallback)
		if !plan.enabled {
			delete(svc.randomScheduleByChan, ch.ID)
			continue
		}

		lastExecution := svc.lastExecutionByChan[ch.ID]

		switch plan.mode {
		case objects.ChannelProbeIntervalModeRandom:
			schedule := svc.randomScheduleByChan[ch.ID]
			interval := schedule.interval
			if interval <= 0 {
				interval = sampleRandomProbeInterval(plan.randomMin, plan.randomMax)
			}

			if lastExecution.IsZero() {
				svc.lastExecutionByChan[ch.ID] = now
				nextInterval := sampleRandomProbeInterval(plan.randomMin, plan.randomMax)
				svc.randomScheduleByChan[ch.ID] = channelRandomSchedule{
					interval:        nextInterval,
					nextExecutionAt: now.Add(nextInterval),
				}
				windows = append(windows, dueChannelProbeWindow{
					channelID: ch.ID,
					startTime: now.Add(-interval),
					endTime:   now,
					interval:  interval,
					mode:      objects.ChannelProbeIntervalModeRandom,
				})
				continue
			}

			if schedule.nextExecutionAt.IsZero() {
				schedule = channelRandomSchedule{
					interval:        interval,
					nextExecutionAt: lastExecution.Add(interval),
				}
				svc.randomScheduleByChan[ch.ID] = schedule
			}

			if now.Before(schedule.nextExecutionAt) {
				continue
			}

			svc.lastExecutionByChan[ch.ID] = now
			nextInterval := sampleRandomProbeInterval(plan.randomMin, plan.randomMax)
			svc.randomScheduleByChan[ch.ID] = channelRandomSchedule{
				interval:        nextInterval,
				nextExecutionAt: now.Add(nextInterval),
			}
			windows = append(windows, dueChannelProbeWindow{
				channelID: ch.ID,
				startTime: now.Add(-schedule.interval),
				endTime:   now,
				interval:  schedule.interval,
				mode:      objects.ChannelProbeIntervalModeRandom,
			})
		default:
			delete(svc.randomScheduleByChan, ch.ID)
			if !lastExecution.IsZero() && now.Before(lastExecution.Add(plan.fixedInterval)) {
				continue
			}

			svc.lastExecutionByChan[ch.ID] = now
			windows = append(windows, dueChannelProbeWindow{
				channelID: ch.ID,
				startTime: now.Add(-plan.fixedInterval),
				endTime:   now,
				interval:  plan.fixedInterval,
				mode:      objects.ChannelProbeIntervalModeFixed,
			})
		}
	}

	return windows
}

type channelProbeStats struct {
	total                 int
	success               int
	avgTokensPerSecond    *float64
	avgTimeToFirstTokenMs *float64
}

// computeAllChannelProbeStats computes probe stats for all channels in a single batch query.
// Uses CTE with ROW_NUMBER to get only successful execution per request, includes all token types,
// and applies different TPS formulas for streaming vs non-streaming.
func (svc *ChannelProbeService) computeAllChannelProbeStats(
	ctx context.Context,
	channelIDs []int,
	startTime time.Time,
	endTime time.Time,
) (map[int]*channelProbeStats, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}

	// Use raw SQL query with CTE pattern (same as Task 1 FastestChannels)
	type probeResult struct {
		ChannelID              int   `json:"channel_id"`
		TotalCount             int   `json:"total_count"`
		SuccessCount           int   `json:"success_count"`
		TotalTokens            int64 `json:"total_tokens"`
		EffectiveLatencyMs     int64 `json:"effective_latency_ms"`
		TotalFirstTokenLatency int64 `json:"total_first_token_latency"`
		RequestCount           int   `json:"request_count"`
		StreamingRequestCount  int   `json:"streaming_request_count"`
	}

	dbDriver := svc.db.Driver()
	sqlDB, ok := dbDriver.(*entsql.Driver)
	if !ok {
		return nil, fmt.Errorf("failed to get underlying SQL driver")
	}

	// Detect dialect to use appropriate placeholder syntax
	// PostgreSQL uses $1, $2, etc. while SQLite uses ? placeholders
	dialectName := sqlDB.Dialect()
	useDollarPlaceholders := dialectName == dialect.Postgres

	// Build args slice for parameterized query
	args := make([]interface{}, 0, len(channelIDs)+2)
	args = append(args, startTime.UTC(), endTime.UTC())

	// Build channel ID filter with dialect-aware parameterized placeholders
	// Note: Placeholders start at $3 because $1 and $2 are reserved for startTime and endTime timestamps.
	// The args slice is constructed with timestamps first (lines 155-156), then channel IDs appended,
	// so placeholder numbering must match this ordering to bind values correctly.
	channelIDFilter := ""
	if len(channelIDs) > 0 {
		placeholders := make([]string, len(channelIDs))
		for i, id := range channelIDs {
			if useDollarPlaceholders {
				placeholders[i] = fmt.Sprintf("$%d", i+3) // $3, $4, etc. for PostgreSQL (offset by 2 for timestamps)
			} else {
				placeholders[i] = "?" // ? for SQLite
			}
			args = append(args, id)
		}
		channelIDFilter = fmt.Sprintf("AND se.channel_id IN (%s)", strings.Join(placeholders, ","))
	}

	queryMode := qb.ThroughputModeRowNumber
	if !useDollarPlaceholders {
		queryMode = qb.ThroughputModeMaxID
	}

	query := qb.BuildProbeStatsQuery(useDollarPlaceholders, channelIDFilter, queryMode)

	rows, err := sqlDB.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query channel probe stats: %w", err)
	}

	defer func() { _ = rows.Close() }()

	result := make(map[int]*channelProbeStats)
	for rows.Next() {
		var r probeResult
		if err := rows.Scan(
			&r.ChannelID,
			&r.TotalCount,
			&r.SuccessCount,
			&r.TotalTokens,
			&r.EffectiveLatencyMs,
			&r.TotalFirstTokenLatency,
			&r.RequestCount,
			&r.StreamingRequestCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan probe result: %w", err)
		}

		stats := &channelProbeStats{
			total:   r.TotalCount,
			success: r.SuccessCount,
		}

		// Calculate avg tokens per second using effective latency
		// For streaming: tokens / ((latency - first_token_latency) / 1000)
		// For non-streaming: tokens / (latency / 1000)
		if r.TotalTokens > 0 && r.EffectiveLatencyMs > 0 {
			tps := float64(r.TotalTokens) / (float64(r.EffectiveLatencyMs) / 1000.0)
			stats.avgTokensPerSecond = &tps
		}

		// Calculate avg time to first token (only for streaming requests)
		if r.TotalFirstTokenLatency > 0 && r.StreamingRequestCount > 0 {
			avgTTFT := float64(r.TotalFirstTokenLatency) / float64(r.StreamingRequestCount)
			stats.avgTimeToFirstTokenMs = &avgTTFT
		}

		result[r.ChannelID] = stats
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating probe results: %w", err)
	}

	return result, nil
}

// runProbe executes the probe task.
func (svc *ChannelProbeService) runProbe(ctx context.Context) {
	setting := svc.SystemService.ChannelSettingOrDefault(ctx)
	if !setting.Probe.Enabled {
		log.Debug(ctx, "Channel probe is disabled, skipping")
		return
	}

	now := xtime.UTCNow()
	ctx = ent.NewContext(ctx, svc.db)

	// Get all enabled channels
	channels, err := svc.db.Channel.Query().
		Where(channel.StatusEQ(channel.StatusEnabled)).
		All(ctx)
	if err != nil {
		log.Error(ctx, "Failed to query enabled channels", log.Cause(err))
		return
	}

	if len(channels) == 0 {
		log.Debug(ctx, "No enabled channels to probe")
		return
	}

	windows := svc.collectDueChannelProbeWindows(channels, setting.Probe.Frequency, now)

	if len(windows) == 0 {
		log.Debug(ctx, "Skipping probe, no channel probe window is due")
		return
	}

	groupedWindows := make(map[string][]dueChannelProbeWindow)
	for _, window := range windows {
		key := fmt.Sprintf("%d:%d", window.startTime.Unix(), window.endTime.Unix())
		groupedWindows[key] = append(groupedWindows[key], window)
	}

	for _, windowGroup := range groupedWindows {
		window := windowGroup[0]
		timestamp := window.endTime.Unix()
		channelIDs := lo.Map(windowGroup, func(item dueChannelProbeWindow, _ int) int {
			return item.channelID
		})

		log.Debug(ctx, "Starting channel probe group",
			log.String("mode", string(window.mode)),
			log.Duration("interval", window.interval),
			log.Int64("timestamp", timestamp),
			log.Int("channels", len(channelIDs)),
		)

		allStats, err := svc.computeAllChannelProbeStats(ctx, channelIDs, window.startTime, window.endTime)
		if err != nil {
			log.Error(ctx, "Failed to compute channel probe stats",
				log.String("mode", string(window.mode)),
				log.Cause(err),
			)
			continue
		}

		var probes []*ent.ChannelProbeCreate

		for _, channelID := range channelIDs {
			stats, ok := allStats[channelID]
			if !ok || stats.total == 0 {
				activeStats := svc.runActiveProbeForChannel(ctx, channelID)
				if activeStats != nil {
					stats = activeStats
					ok = true
				}
			}

			if !ok || stats.total == 0 {
				continue
			}

			probes = append(probes, svc.db.ChannelProbe.Create().
				SetChannelID(channelID).
				SetTotalRequestCount(stats.total).
				SetSuccessRequestCount(stats.success).
				SetNillableAvgTokensPerSecond(stats.avgTokensPerSecond).
				SetNillableAvgTimeToFirstTokenMs(stats.avgTimeToFirstTokenMs).
				SetTimestamp(timestamp),
			)
		}

		if len(probes) == 0 {
			log.Debug(ctx, "No probe data to store for channel probe group",
				log.String("mode", string(window.mode)),
				log.Int64("timestamp", timestamp),
			)
			continue
		}

		if err := svc.db.ChannelProbe.CreateBulk(probes...).Exec(ctx); err != nil {
			log.Error(ctx, "Failed to create channel probes",
				log.String("mode", string(window.mode)),
				log.Cause(err),
			)
			continue
		}

		log.Debug(ctx, "Channel probe group completed",
			log.String("mode", string(window.mode)),
			log.Int("channels_probed", len(probes)),
			log.Int64("timestamp", timestamp),
		)
	}
}

func (svc *ChannelProbeService) runActiveProbeForChannel(ctx context.Context, channelID int) *channelProbeStats {
	if svc.ChannelService == nil {
		return nil
	}

	ch := svc.ChannelService.GetEnabledChannel(channelID)
	if ch == nil || ch.Outbound == nil || ch.HTTPClient == nil {
		return &channelProbeStats{total: 1, success: 0}
	}

	modelID := pickProbeModel(ch)
	if modelID == "" {
		return &channelProbeStats{total: 1, success: 0}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	start := time.Now()
	req := buildActiveProbeRequest(ch, modelID)
	if req == nil {
		svc.recordProbeResult(ctx, channelID, modelID, false)
		return &channelProbeStats{total: 1, success: 0}
	}

	rawReq, err := ch.Outbound.TransformRequest(probeCtx, req)
	if err != nil {
		log.Warn(probeCtx, "active channel probe request build failed",
			log.Int("channel_id", channelID),
			log.String("channel_name", ch.Name),
			log.String("model", modelID),
			log.Cause(err),
		)
		svc.recordProbeResult(ctx, channelID, modelID, false)
		return &channelProbeStats{total: 1, success: 0}
	}

	var stats *channelProbeStats
	if req.Stream != nil && *req.Stream {
		stats = svc.runActiveStreamProbe(probeCtx, ch, rawReq, start, modelID)
	} else {
		stats = svc.runActiveUnaryProbe(probeCtx, ch, rawReq, start, modelID)
	}

	if stats != nil {
		svc.recordProbeResult(ctx, channelID, modelID, stats.success > 0)
	}

	return stats
}

// recordProbeResult reports the probe outcome to the circuit breaker so the
// load balancer can immediately reflect channel health in routing decisions.
func (svc *ChannelProbeService) recordProbeResult(ctx context.Context, channelID int, modelID string, success bool) {
	if svc.ModelCircuitBreaker == nil {
		return
	}

	if success {
		svc.ModelCircuitBreaker.RecordSuccess(ctx, channelID, modelID)
		log.Debug(ctx, "probe succeeded, circuit breaker updated",
			log.Int("channel_id", channelID),
			log.String("model_id", modelID),
		)
	} else {
		svc.ModelCircuitBreaker.RecordError(ctx, channelID, modelID)
		log.Warn(ctx, "probe failed, circuit breaker updated",
			log.Int("channel_id", channelID),
			log.String("model_id", modelID),
		)
	}
}

func (svc *ChannelProbeService) runActiveUnaryProbe(
	ctx context.Context,
	ch *Channel,
	rawReq *httpclient.Request,
	start time.Time,
	modelID string,
) *channelProbeStats {
	rawResp, err := ch.HTTPClient.Do(ctx, rawReq)
	if err != nil {
		log.Warn(ctx, "active channel probe request failed",
			log.Int("channel_id", ch.ID),
			log.String("channel_name", ch.Name),
			log.String("model", modelID),
			log.Cause(err),
		)
		return &channelProbeStats{total: 1, success: 0}
	}

	_, err = ch.Outbound.TransformResponse(ctx, rawResp)
	if err != nil {
		log.Warn(ctx, "active channel probe response transform failed",
			log.Int("channel_id", ch.ID),
			log.String("channel_name", ch.Name),
			log.String("model", modelID),
			log.Cause(err),
		)
		return &channelProbeStats{total: 1, success: 0}
	}

	latencyMs := float64(time.Since(start).Milliseconds())

	return &channelProbeStats{
		total:                 1,
		success:               1,
		avgTimeToFirstTokenMs: &latencyMs,
	}
}

func (svc *ChannelProbeService) runActiveStreamProbe(
	ctx context.Context,
	ch *Channel,
	rawReq *httpclient.Request,
	start time.Time,
	modelID string,
) *channelProbeStats {
	rawStream, err := ch.HTTPClient.DoStream(ctx, rawReq)
	if err != nil {
		log.Warn(ctx, "active channel probe stream request failed",
			log.Int("channel_id", ch.ID),
			log.String("channel_name", ch.Name),
			log.String("model", modelID),
			log.Cause(err),
		)
		return &channelProbeStats{total: 1, success: 0}
	}
	defer func() { _ = rawStream.Close() }()

	stream, err := ch.Outbound.TransformStream(ctx, rawStream)
	if err != nil {
		log.Warn(ctx, "active channel probe stream transform failed",
			log.Int("channel_id", ch.ID),
			log.String("channel_name", ch.Name),
			log.String("model", modelID),
			log.Cause(err),
		)
		return &channelProbeStats{total: 1, success: 0}
	}
	defer func() { _ = stream.Close() }()

	var firstChunkAt *time.Time
	for stream.Next() {
		chunk := stream.Current()
		if chunk == nil || chunk.Object == "[DONE]" {
			continue
		}

		if firstChunkAt == nil {
			now := time.Now()
			firstChunkAt = &now
		}
	}

	if stream.Err() != nil {
		log.Warn(ctx, "active channel probe stream iteration failed",
			log.Int("channel_id", ch.ID),
			log.String("channel_name", ch.Name),
			log.String("model", modelID),
			log.Cause(stream.Err()),
		)
		return &channelProbeStats{total: 1, success: 0}
	}

	latencyMs := float64(time.Since(start).Milliseconds())
	if firstChunkAt != nil {
		latencyMs = float64(firstChunkAt.Sub(start).Milliseconds())
	}

	return &channelProbeStats{
		total:                 1,
		success:               1,
		avgTimeToFirstTokenMs: &latencyMs,
	}
}

func buildActiveProbeRequest(ch *Channel, modelID string) *llm.Request {
	if strings.TrimSpace(modelID) == "" {
		return nil
	}

	streamPreferred := true
	if ch != nil && ch.Policies.Stream == objects.CapabilityPolicyForbid {
		streamPreferred = false
	}

	systemText := "You are a health probe. Reply with OK only."
	userText := "Reply with OK only."

	return &llm.Request{
		Model: modelID,
		Messages: []llm.Message{
			{
				Role: "system",
				Content: llm.MessageContent{
					Content: lo.ToPtr(systemText),
				},
			},
			{
				Role: "user",
				Content: llm.MessageContent{
					Content: lo.ToPtr(userText),
				},
			},
		},
		Metadata: map[string]string{
			"user_id":    "channel-probe-user",
			"session_id": "channel-probe-session",
		},
		MaxTokens:           lo.ToPtr(int64(8)),
		MaxCompletionTokens: lo.ToPtr(int64(8)),
		Temperature:         lo.ToPtr(0.0),
		Stream:              lo.ToPtr(streamPreferred),
	}
}

func pickProbeModel(ch *Channel) string {
	if ch == nil {
		return ""
	}

	if strings.TrimSpace(ch.DefaultTestModel) != "" {
		return ch.DefaultTestModel
	}

	for _, modelID := range ch.SupportedModels {
		if strings.TrimSpace(modelID) != "" {
			return modelID
		}
	}

	for modelID := range ch.GetModelEntries() {
		if strings.TrimSpace(modelID) != "" {
			return modelID
		}
	}

	return ""
}

const channelProbeHistoryLimit = 15

// QueryChannelProbes queries recent probe data for multiple channels.
func (svc *ChannelProbeService) QueryChannelProbes(ctx context.Context, channelIDs []int) ([]*ChannelProbeData, error) {
	_, err := authz.RunWithScopeDecision(ctx, scopes.ScopeReadChannels, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		return nil, err
	}

	probes, err := svc.db.ChannelProbe.Query().
		Where(channelprobe.ChannelIDIn(channelIDs...)).
		Order(ent.Desc(channelprobe.FieldTimestamp)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	probeMap := make(map[int][]*ChannelProbePoint, len(channelIDs))
	for _, p := range probes {
		points := probeMap[p.ChannelID]
		if len(points) >= channelProbeHistoryLimit {
			continue
		}
		probeMap[p.ChannelID] = append(points, &ChannelProbePoint{
			Timestamp:             p.Timestamp,
			TotalRequestCount:     p.TotalRequestCount,
			SuccessRequestCount:   p.SuccessRequestCount,
			AvgTokensPerSecond:    p.AvgTokensPerSecond,
			AvgTimeToFirstTokenMs: p.AvgTimeToFirstTokenMs,
		})
	}

	result := make([]*ChannelProbeData, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		points := probeMap[channelID]
		lo.Reverse(points)
		result = append(result, &ChannelProbeData{
			ChannelID: channelID,
			Points:    points,
		})
	}

	return result, nil
}

// RunProbeNow manually triggers the probe task.
func (svc *ChannelProbeService) RunProbeNow(ctx context.Context) {
	svc.runProbe(ctx)
}

// GetProbesByChannelID returns probe data for a single channel.
func (svc *ChannelProbeService) GetProbesByChannelID(ctx context.Context, channelID int) ([]*ChannelProbePoint, error) {
	data, err := svc.QueryChannelProbes(ctx, []int{channelID})
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return []*ChannelProbePoint{}, nil
	}

	return data[0].Points, nil
}

// GetChannelProbeDataInput is the input for batch query.
type GetChannelProbeDataInput struct {
	ChannelIDs []int `json:"channel_ids"`
}

// BatchQueryChannelProbes is an alias for QueryChannelProbes for GraphQL.
func (svc *ChannelProbeService) BatchQueryChannelProbes(ctx context.Context, input GetChannelProbeDataInput) ([]*ChannelProbeData, error) {
	if len(input.ChannelIDs) == 0 {
		return []*ChannelProbeData{}, nil
	}

	return svc.QueryChannelProbes(ctx, input.ChannelIDs)
}
