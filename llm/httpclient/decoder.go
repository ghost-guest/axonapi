package httpclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/tmaxmax/go-sse"
)

// defaultSSEIdleTimeout is the maximum time to wait for the next SSE event before
// treating the connection as hung. Claude and other providers occasionally stop
// sending data without closing the connection; this catches that case.
const defaultSSEIdleTimeout = 5 * time.Minute

// decoderRegistry holds registered stream decoders.
type decoderRegistry struct {
	mu       sync.RWMutex
	decoders map[string]StreamDecoderFactory
}

// globalRegistry is the global decoder registry.
var globalRegistry = &decoderRegistry{
	decoders: make(map[string]StreamDecoderFactory),
}

// RegisterDecoder registers a stream decoder for a specific content type.
func RegisterDecoder(contentType string, factory StreamDecoderFactory) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	globalRegistry.decoders[contentType] = factory
}

// GetDecoder returns a decoder factory for the given content type.
func GetDecoder(contentType string) (StreamDecoderFactory, bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	factory, exists := globalRegistry.decoders[contentType]

	return factory, exists
}

// sseRecvResult carries a single Recv() result from the background goroutine.
type sseRecvResult struct {
	event sse.Event
	err   error
}

// NewDefaultSSEDecoder creates a new default SSE decoder.
//
// The decoder spawns a single background goroutine that calls sseStream.Recv()
// and forwards results via a buffered channel. Next() then uses select{} to
// race that channel against ctx.Done() and an idle timer, so a hung upstream
// (e.g. Claude sends no data for minutes) is caught and reported as an error
// rather than blocking forever.
func NewDefaultSSEDecoder(ctx context.Context, rc io.ReadCloser) StreamDecoder {
	sseStream := sse.NewStreamWithConfig(rc, &sse.StreamConfig{
		// Large event size for image generation payloads.
		MaxEventSize: 32 * 1024 * 1024,
	})

	// Buffered by 1 so the goroutine never blocks between Next() calls.
	recvCh := make(chan sseRecvResult, 1)

	d := &defaultSSEDecoder{
		ctx:       ctx,
		sseStream: sseStream,
		recvCh:    recvCh,
	}

	// Background reader: runs until EOF, error, or the stream is closed.
	go func() {
		for {
			event, err := sseStream.Recv()
			select {
			case recvCh <- sseRecvResult{event: event, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	return d
}

// Ensure defaultSSEDecoder implements StreamDecoder.
var _ StreamDecoder = (*defaultSSEDecoder)(nil)

// defaultSSEDecoder implements streams.Stream for Server-Sent Events using go-sse Stream.
//
// Next() is non-blocking with respect to context cancellation: it races the
// background Recv() goroutine against ctx.Done() and defaultSSEIdleTimeout so
// a silent upstream can never hang the caller indefinitely.
//
//nolint:containedctx // Checked.
type defaultSSEDecoder struct {
	ctx       context.Context
	sseStream *sse.Stream
	recvCh    chan sseRecvResult
	current   *StreamEvent
	err       error

	// NOT concurrency-safe: do not call Next/Close from multiple goroutines.
	// Close is made idempotent (safe to call multiple times sequentially).
	closed   bool
	closeErr error
}

// Next advances to the next event in the stream.
//
// It blocks until one of:
//  1. A new event arrives from the upstream SSE stream.
//  2. The request context is cancelled / times out.
//  3. defaultSSEIdleTimeout elapses with no data (upstream hung silently).
func (s *defaultSSEDecoder) Next() bool {
	if s.err != nil {
		return false
	}

	if s.closed {
		return false
	}

	idleTimer := time.NewTimer(defaultSSEIdleTimeout)
	defer idleTimer.Stop()

	select {
	case <-s.ctx.Done():
		slog.DebugContext(s.ctx, "SSE stream cancelled by context")
		s.err = s.ctx.Err()
		_ = s.Close()
		return false

	case <-idleTimer.C:
		slog.WarnContext(s.ctx, "SSE stream idle timeout exceeded, treating upstream as hung",
			slog.Duration("timeout", defaultSSEIdleTimeout))
		s.err = context.DeadlineExceeded
		_ = s.Close()
		return false

	case result := <-s.recvCh:
		if result.err != nil {
			if errors.Is(result.err, io.EOF) {
				slog.DebugContext(s.ctx, "SSE stream reached EOF")
				_ = s.Close()
				return false
			}
			s.err = result.err
			_ = s.Close()
			return false
		}

		slog.DebugContext(s.ctx, "SSE event received", slog.Any("event", result.event))
		s.current = &StreamEvent{
			LastEventID: result.event.LastEventID,
			Type:        result.event.Type,
			Data:        []byte(result.event.Data),
		}
		return true
	}
}

// Current returns the current event data.
func (s *defaultSSEDecoder) Current() *StreamEvent {
	return s.current
}

// Err returns any error that occurred during streaming.
func (s *defaultSSEDecoder) Err() error {
	return s.err
}

// Close closes the stream and releases resources.
func (s *defaultSSEDecoder) Close() error {
	// NOT concurrency-safe: callers must not call Close concurrently with Next.
	if s.closed {
		return s.closeErr
	}

	s.closed = true
	if s.sseStream != nil {
		// Closing the underlying stream unblocks sseStream.Recv() in the
		// background goroutine, allowing it to exit cleanly.
		s.closeErr = s.sseStream.Close()
		slog.DebugContext(s.ctx, "SSE stream closed")
	}

	return s.closeErr
}

// init registers the default SSE decoder.
func init() {
	RegisterDecoder("text/event-stream", NewDefaultSSEDecoder)
	RegisterDecoder("text/event-stream; charset=utf-8", NewDefaultSSEDecoder)
}
