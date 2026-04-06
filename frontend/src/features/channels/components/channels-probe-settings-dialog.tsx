'use client';

import { useEffect, useMemo, useState } from 'react';
import { z } from 'zod';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { useUpdateChannel } from '../data/channels';
import { Channel, ChannelProbeFrequency, ChannelProbeIntervalMode } from '../data/schema';
import { mergeChannelSettingsForUpdate } from '../utils/merge';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: Channel;
}

const FOLLOW_SYSTEM_PROBE_INTERVAL_MODE = '__system__';
const DEFAULT_FIXED_PROBE_INTERVAL_SECONDS = 60 * 60;
const DEFAULT_RANDOM_PROBE_MIN_SECONDS = 30 * 60;
const DEFAULT_RANDOM_PROBE_MAX_SECONDS = 2 * 60 * 60;
const PROBE_INTERVAL_UNIT_SECONDS = {
  minutes: 60,
  hours: 60 * 60,
  days: 24 * 60 * 60,
} as const;

type ProbeIntervalUnit = keyof typeof PROBE_INTERVAL_UNIT_SECONDS;

const probeSettingsSchema = z
  .object({
    probeEnabled: z.boolean(),
    probeFrequency: z.string().optional(),
    probeIntervalMode: z.enum(['', 'fixed', 'random']),
    probeFixedIntervalSeconds: z.number().int().positive().optional(),
    probeRandomMinIntervalSeconds: z.number().int().positive().optional(),
    probeRandomMaxIntervalSeconds: z.number().int().positive().optional(),
  })
  .superRefine((data, ctx) => {
    if (!data.probeEnabled) {
      return;
    }

    if (data.probeIntervalMode === 'fixed' && !(data.probeFixedIntervalSeconds && data.probeFixedIntervalSeconds > 0)) {
      ctx.addIssue({
        code: 'custom',
        message: 'Fixed probe interval must be greater than 0 seconds',
        path: ['probeFixedIntervalSeconds'],
      });
    }

    if (data.probeIntervalMode === 'random') {
      if (!(data.probeRandomMinIntervalSeconds && data.probeRandomMinIntervalSeconds > 0)) {
        ctx.addIssue({
          code: 'custom',
          message: 'Random probe minimum interval must be greater than 0 seconds',
          path: ['probeRandomMinIntervalSeconds'],
        });
      }

      if (!(data.probeRandomMaxIntervalSeconds && data.probeRandomMaxIntervalSeconds > 0)) {
        ctx.addIssue({
          code: 'custom',
          message: 'Random probe maximum interval must be greater than 0 seconds',
          path: ['probeRandomMaxIntervalSeconds'],
        });
      }

      if (
        data.probeRandomMinIntervalSeconds &&
        data.probeRandomMaxIntervalSeconds &&
        data.probeRandomMaxIntervalSeconds < data.probeRandomMinIntervalSeconds
      ) {
        ctx.addIssue({
          code: 'custom',
          message: 'Random probe maximum interval must be greater than or equal to minimum interval',
          path: ['probeRandomMaxIntervalSeconds'],
        });
      }
    }
  });

const PROBE_INTERVAL_MODE_OPTIONS: Array<{ value: ChannelProbeIntervalMode; labelKey: string }> = [
  { value: '', labelKey: 'channels.dialogs.fields.probeIntervalMode.options.system' },
  { value: 'fixed', labelKey: 'channels.dialogs.fields.probeIntervalMode.options.fixed' },
  { value: 'random', labelKey: 'channels.dialogs.fields.probeIntervalMode.options.random' },
];

const PROBE_INTERVAL_UNIT_OPTIONS: Array<{ value: ProbeIntervalUnit; labelKey: string }> = [
  { value: 'minutes', labelKey: 'channels.dialogs.fields.probeIntervalUnit.options.minutes' },
  { value: 'hours', labelKey: 'channels.dialogs.fields.probeIntervalUnit.options.hours' },
  { value: 'days', labelKey: 'channels.dialogs.fields.probeIntervalUnit.options.days' },
];

function secondsFromLegacyProbeFrequency(frequency?: ChannelProbeFrequency | null): number | undefined {
  switch (frequency) {
    case '1m':
      return 60;
    case '5m':
      return 5 * 60;
    case '30m':
      return 30 * 60;
    case '1h':
      return 60 * 60;
    default:
      return undefined;
  }
}

function splitProbeInterval(seconds?: number | null): { value: number; unit: ProbeIntervalUnit } {
  const normalizedSeconds = Math.max(0, Math.trunc(seconds || 0));
  if (normalizedSeconds > 0 && normalizedSeconds % PROBE_INTERVAL_UNIT_SECONDS.days === 0) {
    return { value: normalizedSeconds / PROBE_INTERVAL_UNIT_SECONDS.days, unit: 'days' };
  }
  if (normalizedSeconds > 0 && normalizedSeconds % PROBE_INTERVAL_UNIT_SECONDS.hours === 0) {
    return { value: normalizedSeconds / PROBE_INTERVAL_UNIT_SECONDS.hours, unit: 'hours' };
  }
  if (normalizedSeconds > 0 && normalizedSeconds % PROBE_INTERVAL_UNIT_SECONDS.minutes === 0) {
    return { value: normalizedSeconds / PROBE_INTERVAL_UNIT_SECONDS.minutes, unit: 'minutes' };
  }
  return { value: Math.max(1, Math.ceil(normalizedSeconds / PROBE_INTERVAL_UNIT_SECONDS.minutes)), unit: 'minutes' };
}

function intervalToSeconds(value: number, unit: ProbeIntervalUnit): number {
  const normalizedValue = Math.max(0, Math.trunc(value || 0));
  return normalizedValue * PROBE_INTERVAL_UNIT_SECONDS[unit];
}

function normalizeProbeSettings(settings?: Channel['settings'] | null) {
  const legacyFixedInterval = secondsFromLegacyProbeFrequency(settings?.probeFrequency);
  const mode = (settings?.probeIntervalMode || (legacyFixedInterval ? 'fixed' : '')) as ChannelProbeIntervalMode;

  return {
    probeEnabled: settings?.probeEnabled ?? true,
    probeFrequency: settings?.probeFrequency || '',
    probeIntervalMode: mode,
    probeFixedIntervalSeconds: settings?.probeFixedIntervalSeconds ?? legacyFixedInterval ?? DEFAULT_FIXED_PROBE_INTERVAL_SECONDS,
    probeRandomMinIntervalSeconds: settings?.probeRandomMinIntervalSeconds ?? DEFAULT_RANDOM_PROBE_MIN_SECONDS,
    probeRandomMaxIntervalSeconds: settings?.probeRandomMaxIntervalSeconds ?? DEFAULT_RANDOM_PROBE_MAX_SECONDS,
  };
}

export function ChannelsProbeSettingsDialog({ open, onOpenChange, currentRow }: Props) {
  const { t } = useTranslation();
  const updateChannel = useUpdateChannel();
  const normalizedSettings = useMemo(() => normalizeProbeSettings(currentRow.settings), [currentRow.settings]);
  const initialFixedInterval = useMemo(
    () => splitProbeInterval(normalizedSettings.probeFixedIntervalSeconds),
    [normalizedSettings.probeFixedIntervalSeconds]
  );
  const initialRandomMinInterval = useMemo(
    () => splitProbeInterval(normalizedSettings.probeRandomMinIntervalSeconds),
    [normalizedSettings.probeRandomMinIntervalSeconds]
  );
  const initialRandomMaxInterval = useMemo(
    () => splitProbeInterval(normalizedSettings.probeRandomMaxIntervalSeconds),
    [normalizedSettings.probeRandomMaxIntervalSeconds]
  );
  const [fixedProbeValue, setFixedProbeValue] = useState(initialFixedInterval.value);
  const [fixedProbeUnit, setFixedProbeUnit] = useState<ProbeIntervalUnit>(initialFixedInterval.unit);
  const [randomMinValue, setRandomMinValue] = useState(initialRandomMinInterval.value);
  const [randomMinUnit, setRandomMinUnit] = useState<ProbeIntervalUnit>(initialRandomMinInterval.unit);
  const [randomMaxValue, setRandomMaxValue] = useState(initialRandomMaxInterval.value);
  const [randomMaxUnit, setRandomMaxUnit] = useState<ProbeIntervalUnit>(initialRandomMaxInterval.unit);

  const form = useForm<z.infer<typeof probeSettingsSchema>>({
    resolver: zodResolver(probeSettingsSchema),
    defaultValues: normalizedSettings,
  });

  useEffect(() => {
    form.reset(normalizedSettings);
    setFixedProbeValue(initialFixedInterval.value);
    setFixedProbeUnit(initialFixedInterval.unit);
    setRandomMinValue(initialRandomMinInterval.value);
    setRandomMinUnit(initialRandomMinInterval.unit);
    setRandomMaxValue(initialRandomMaxInterval.value);
    setRandomMaxUnit(initialRandomMaxInterval.unit);
  }, [form, initialFixedInterval, initialRandomMaxInterval, initialRandomMinInterval, normalizedSettings]);

  const probeEnabled = form.watch('probeEnabled') ?? true;
  const probeIntervalMode = form.watch('probeIntervalMode') ?? '';

  const onSubmit = async (values: z.infer<typeof probeSettingsSchema>) => {
    try {
      const nextSettings = mergeChannelSettingsForUpdate(currentRow.settings, {
        probeEnabled: values.probeEnabled,
        probeFrequency: '',
        probeIntervalMode: values.probeIntervalMode,
        probeFixedIntervalSeconds: values.probeFixedIntervalSeconds,
        probeRandomMinIntervalSeconds: values.probeRandomMinIntervalSeconds,
        probeRandomMaxIntervalSeconds: values.probeRandomMaxIntervalSeconds,
      });

      await updateChannel.mutateAsync({
        id: currentRow.id,
        input: {
          settings: nextSettings,
        },
      });

      toast.success(t('channels.messages.updateSuccess'));
      onOpenChange(false);
    } catch (_error) {
      toast.error(t('channels.messages.updateError'));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader className='text-left'>
          <DialogTitle>{t('channels.dialogs.probeSettings.title')}</DialogTitle>
          <DialogDescription>{t('channels.dialogs.probeSettings.description', { name: currentRow.name })}</DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form className='space-y-6' onSubmit={form.handleSubmit(onSubmit)}>
            <FormField
              control={form.control}
              name='probeEnabled'
              render={({ field }) => (
                <FormItem>
                  <div className='flex items-center justify-between gap-4 rounded-lg border p-4'>
                    <div>
                      <FormLabel>{t('channels.dialogs.fields.probeEnabled.label')}</FormLabel>
                      <FormDescription>{t('channels.dialogs.fields.probeEnabled.description')}</FormDescription>
                    </div>
                    <FormControl>
                      <Switch checked={field.value} onCheckedChange={field.onChange} />
                    </FormControl>
                  </div>
                  <FormMessage />
                </FormItem>
              )}
            />

            {probeEnabled && (
              <>
                <FormField
                  control={form.control}
                  name='probeIntervalMode'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('channels.dialogs.fields.probeIntervalMode.label')}</FormLabel>
                      <Select
                        value={field.value || FOLLOW_SYSTEM_PROBE_INTERVAL_MODE}
                        onValueChange={(value) => {
                          const nextValue = value === FOLLOW_SYSTEM_PROBE_INTERVAL_MODE ? '' : value;
                          field.onChange(nextValue);
                          form.setValue('probeFrequency', '', { shouldDirty: true });
                        }}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue placeholder={t('channels.dialogs.fields.probeIntervalMode.placeholder')} />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          {PROBE_INTERVAL_MODE_OPTIONS.map((option) => (
                            <SelectItem
                              key={option.value || FOLLOW_SYSTEM_PROBE_INTERVAL_MODE}
                              value={option.value || FOLLOW_SYSTEM_PROBE_INTERVAL_MODE}
                            >
                              {t(option.labelKey)}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <FormDescription>{t('channels.dialogs.fields.probeIntervalMode.description')}</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {probeIntervalMode === 'fixed' && (
                  <FormField
                    control={form.control}
                    name='probeFixedIntervalSeconds'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('channels.dialogs.fields.probeFixedInterval.label')}</FormLabel>
                        <div className='flex gap-2'>
                          <Input
                            type='number'
                            min={1}
                            value={fixedProbeValue}
                            onChange={(event) => {
                              const nextValue = Math.max(1, Number(event.target.value) || 0);
                              setFixedProbeValue(nextValue);
                              field.onChange(intervalToSeconds(nextValue, fixedProbeUnit));
                            }}
                          />
                          <Select
                            value={fixedProbeUnit}
                            onValueChange={(value) => {
                              const nextUnit = value as ProbeIntervalUnit;
                              setFixedProbeUnit(nextUnit);
                              field.onChange(intervalToSeconds(fixedProbeValue, nextUnit));
                            }}
                          >
                            <SelectTrigger className='w-[140px]'>
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {PROBE_INTERVAL_UNIT_OPTIONS.map((option) => (
                                <SelectItem key={option.value} value={option.value}>
                                  {t(option.labelKey)}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>
                        <FormDescription>{t('channels.dialogs.fields.probeFixedInterval.description')}</FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}

                {probeIntervalMode === 'random' && (
                  <>
                    <FormField
                      control={form.control}
                      name='probeRandomMinIntervalSeconds'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('channels.dialogs.fields.probeRandomMinInterval.label')}</FormLabel>
                          <div className='flex gap-2'>
                            <Input
                              type='number'
                              min={1}
                              value={randomMinValue}
                              onChange={(event) => {
                                const nextValue = Math.max(1, Number(event.target.value) || 0);
                                setRandomMinValue(nextValue);
                                field.onChange(intervalToSeconds(nextValue, randomMinUnit));
                              }}
                            />
                            <Select
                              value={randomMinUnit}
                              onValueChange={(value) => {
                                const nextUnit = value as ProbeIntervalUnit;
                                setRandomMinUnit(nextUnit);
                                field.onChange(intervalToSeconds(randomMinValue, nextUnit));
                              }}
                            >
                              <SelectTrigger className='w-[140px]'>
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {PROBE_INTERVAL_UNIT_OPTIONS.map((option) => (
                                  <SelectItem key={option.value} value={option.value}>
                                    {t(option.labelKey)}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </div>
                          <FormDescription>{t('channels.dialogs.fields.probeRandomMinInterval.description')}</FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='probeRandomMaxIntervalSeconds'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('channels.dialogs.fields.probeRandomMaxInterval.label')}</FormLabel>
                          <div className='flex gap-2'>
                            <Input
                              type='number'
                              min={1}
                              value={randomMaxValue}
                              onChange={(event) => {
                                const nextValue = Math.max(1, Number(event.target.value) || 0);
                                setRandomMaxValue(nextValue);
                                field.onChange(intervalToSeconds(nextValue, randomMaxUnit));
                              }}
                            />
                            <Select
                              value={randomMaxUnit}
                              onValueChange={(value) => {
                                const nextUnit = value as ProbeIntervalUnit;
                                setRandomMaxUnit(nextUnit);
                                field.onChange(intervalToSeconds(randomMaxValue, nextUnit));
                              }}
                            >
                              <SelectTrigger className='w-[140px]'>
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {PROBE_INTERVAL_UNIT_OPTIONS.map((option) => (
                                  <SelectItem key={option.value} value={option.value}>
                                    {t(option.labelKey)}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </div>
                          <FormDescription>{t('channels.dialogs.fields.probeRandomMaxInterval.description')}</FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </>
                )}
              </>
            )}

            <DialogFooter>
              <Button type='button' variant='outline' onClick={() => onOpenChange(false)}>
                {t('common.buttons.cancel')}
              </Button>
              <Button type='submit' disabled={updateChannel.isPending}>
                {updateChannel.isPending ? t('common.buttons.save') : t('common.buttons.save')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
