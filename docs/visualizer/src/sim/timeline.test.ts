import { describe, expect, it } from 'vitest';
import { Timeline } from './timeline';

const settings = { producerRates: Array(6).fill(8), linkLimit: 36, lagThreshold: 20 };

/** Play like the page does: uneven frame steps, inputs between frames. */
function play(tl: Timeline, until: number, inputs: Record<number, () => void> = {}): void {
  const frames = [16, 33, 17, 120, 16, 250, 16];
  let t = tl.now;
  let i = 0;
  const due = Object.keys(inputs).map(Number).sort((a, b) => a - b);
  while (t < until) {
    t += frames[i++ % frames.length];
    tl.advanceTo(t);
    while (due.length > 0 && tl.now >= due[0]) inputs[due.shift()!]();
  }
}

const state = (tl: Timeline) => JSON.stringify(tl.sim);

describe('timeline', () => {
  it('a seek lands on exactly the state the run had at that time', () => {
    const tl = new Timeline(settings);
    const seen = new Map<number, string>();
    let t = 0;
    while (t < 60_000) {
      tl.advanceTo((t += 250));
      if (t === 4_000) tl.input({ type: 'start' });
      if (t === 9_000) tl.input({ type: 'setting', key: 'linkLimit', value: 80 });
      if (t % 2_500 === 0) seen.set(tl.now, state(tl));
    }
    for (const [time, expected] of [...seen].reverse()) {
      tl.seek(time);
      expect(tl.now).toBe(time);
      expect(state(tl)).toBe(expected);
    }
  });

  it('replays the recorded inputs when it plays forward again after a rewind', () => {
    const tl = new Timeline(settings);
    play(tl, 90_000, { 3_000: () => tl.input({ type: 'start' }) });
    const endedAt = tl.now;
    const finished = state(tl);
    tl.seek(1_000);
    expect(tl.sim.phase).toBe('idle');
    play(tl, endedAt - 1_000); // replays the Start recorded at 3 s
    tl.advanceTo(endedAt);
    expect(tl.now).toBe(endedAt);
    expect(state(tl)).toBe(finished);
  });

  it('an input after a rewind starts a new branch and drops the old future', () => {
    const tl = new Timeline({ producerRates: Array(6).fill(8), linkLimit: 24, lagThreshold: 0 });
    play(tl, 60_000, { 2_000: () => tl.input({ type: 'start' }) });
    expect(tl.sim.phase).toBe('lag-check'); // the link can't keep up: stuck at the lag check

    tl.seek(2_500); // just after Start
    tl.input({ type: 'setting', key: 'linkLimit', value: 80 });
    expect(tl.end).toBe(tl.now);
    expect(tl.markers.every((m) => m.at <= tl.now)).toBe(true);

    play(tl, 120_000);
    expect(tl.sim.phase).toBe('completed');
    expect(tl.markers.map((m) => m.phase)).toContain('switching');
  });

  it('a seek returns the dots still travelling, with how far each has come', () => {
    const tl = new Timeline(settings);
    play(tl, 10_000);
    const inFlight = tl.seek(7_300);
    expect(inFlight.length).toBeGreaterThan(0);
    for (const { hop, elapsed } of inFlight) {
      expect(elapsed).toBeGreaterThanOrEqual(0);
      expect(elapsed).toBeLessThan(hop.duration);
    }
  });

  it('a producer rate change is an input: a rewind before it undoes it, and replaying keeps it', () => {
    const tl = new Timeline({});
    play(tl, 3_000);
    tl.input({ type: 'producer-rate', producer: 2, value: 17 });
    play(tl, 8_000);
    expect(tl.sim.settings.producerRates[2]).toBe(17);
    tl.seek(2_000);
    expect(tl.sim.settings.producerRates[2]).toBe(4);
    tl.seek(6_000);
    expect(tl.sim.settings.producerRates[2]).toBe(17);
  });

  it('keeps a marker for every phase, even two that start in the same step', () => {
    const tl = new Timeline({ producerRates: Array(6).fill(4), linkLimit: 72 });
    play(tl, 60_000, { 5_000: () => tl.input({ type: 'start' }) });
    const phases = tl.markers.map((m) => m.phase);
    expect(phases).toContain('lag-check');
    expect(phases).toContain('threshold-met');
    expect(new Set(phases).size).toBe(phases.length);
  });

  it('markers record when each step started', () => {
    const tl = new Timeline({ producerRates: Array(6).fill(4), linkLimit: 72 });
    play(tl, 90_000, { 5_000: () => tl.input({ type: 'start' }) });
    const phases = tl.markers.map((m) => m.phase);
    expect(phases).toEqual(['idle', 'started', 'lag-check', 'threshold-met', 'fencing', 'zero-lag', 'promoting', 'promoted', 'switching', 'completed']);
    const times = tl.markers.map((m) => m.at);
    expect([...times].sort((a, b) => a - b)).toEqual(times);
  });
});
