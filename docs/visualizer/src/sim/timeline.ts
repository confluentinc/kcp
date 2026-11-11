// A run of the model that can be rewound. The model is deterministic, so the
// run is its starting settings and timed inputs; any moment can be
// rebuilt by replaying them. Snapshots are only a cache that keeps seeking
// fast on long runs.
//
// Order at any model time T: the model ticks up to T, the snapshot for T (if
// T is on the snapshot grid) is taken, then the inputs recorded at T apply.
// Live play, replay, and seeking all follow it, so they reach the same state.

import { Simulation, type Hop, type Input, type Phase, type Settings } from './model';

export interface TimedInput {
  at: number;
  input: Input;
}

export interface Marker {
  at: number;
  phase: Phase;
  migration: number;
}

/** A dot that was already travelling at the moment a seek landed on. */
export interface InFlight {
  hop: Hop;
  elapsed: number;
}

export const SNAPSHOT_EVERY = 5_000;
// Longer than any hop, so a seek can redraw every dot still travelling.
const IN_FLIGHT_LOOKBACK = 1_000;

export class Timeline {
  sim: Simulation;
  /** The furthest model time this run has reached. */
  end = 0;
  inputs: TimedInput[] = [];
  /** Phase changes over the whole run, including ones after a rewind point. */
  markers: Marker[] = [];

  private snapshots = new Map<number, Simulation>();

  constructor(settings: Partial<Settings>) {
    this.sim = new Simulation(settings);
    this.snapshots.set(0, this.sim.clone());
  }

  get now(): number {
    return this.sim.now;
  }

  /**
   * Record and apply an input at the current time. Behind the end of the run
   * this starts a new branch: everything recorded after now is discarded.
   */
  input(input: Input): void {
    if (this.now < this.end) this.truncate();
    this.inputs.push({ at: this.now, input });
    this.sim.apply(input);
    this.collectMarkers();
  }

  /** Advance to `time`, applying any recorded inputs on the way. Returns the hops that started. */
  advanceTo(time: number): Hop[] {
    const hops: Hop[] = [];
    for (;;) {
      const before = this.now;
      const nextInput = this.inputs.find((i) => i.at > before)?.at ?? Infinity;
      const nextSnapshot = (Math.floor(before / SNAPSHOT_EVERY) + 1) * SNAPSHOT_EVERY;
      hops.push(...this.sim.advanceTo(Math.min(time, nextInput, nextSnapshot)));
      if (this.now === before) break; // less than one model step left before `time`
      if (this.now % SNAPSHOT_EVERY === 0 && !this.snapshots.has(this.now)) this.snapshots.set(this.now, this.sim.clone());
      this.applyInputsAt(this.now);
    }
    this.end = Math.max(this.end, this.now);
    this.collectMarkers();
    return hops;
  }

  /** Jump to any time from 0 to the end of the run. Returns the dots in flight there. */
  seek(time: number): InFlight[] {
    const target = Math.max(0, Math.min(time, this.end));
    const from = Math.floor(Math.max(0, target - IN_FLIGHT_LOOKBACK) / SNAPSHOT_EVERY) * SNAPSHOT_EVERY;
    this.sim = (this.snapshots.get(from) ?? this.snapshots.get(0)!).clone();
    this.applyInputsAt(this.now);
    const hops = this.advanceTo(target);
    return hops.filter((h) => h.at + h.duration > target).map((hop) => ({ hop, elapsed: target - hop.at }));
  }

  private applyInputsAt(time: number): void {
    for (const i of this.inputs) if (i.at === time) this.sim.apply(i.input);
  }

  /** Add the phase changes not marked yet. Two phases can start in the same model step. */
  private collectMarkers(): void {
    for (const c of this.sim.phaseChanges) {
      if (!this.markers.some((m) => m.at === c.at && m.phase === c.phase && m.migration === c.migration)) this.markers.push({ ...c });
    }
  }

  private truncate(): void {
    this.inputs = this.inputs.filter((i) => i.at <= this.now);
    this.markers = this.markers.filter((m) => m.at <= this.now);
    for (const t of [...this.snapshots.keys()]) if (t > this.now) this.snapshots.delete(t);
    this.end = this.now;
  }
}
