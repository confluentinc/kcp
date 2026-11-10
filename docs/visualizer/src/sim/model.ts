// A model of a kcp topic-based (dynamic) migration through Confluent Gateway.
// It is a teaching model, not a replay of real data: the stages, the order of
// events, and the cause and effect follow kcp, and the timings are scaled to
// be watchable. The route binds both clusters; each run of kcp, a migration,
// moves the topics selected for it, and every other topic keeps flowing.
//
// The Gateway is one proxy: it sends each topic's traffic to the source or
// the destination, or blocks it, by its route rules. It takes on a change to
// those rules at once; how it does so isn't drawn.
//
// The model owns time. The view advances it and draws what it reports, so the
// same model runs headless in tests.

export type ClusterId = 'source' | 'destination';

export type MirrorState = 'ACTIVE' | 'PENDING_STOPPED' | 'STOPPED';

// Each phase starts at the moment of a change a viewer can see; the step hold
// (timing.stageHold) only spaces out the changes that follow.
export type Phase =
  | 'idle'
  | 'started' // the migration has started; kcp starts the lag check after a short pause (timing.startHold)
  | 'lag-check' // waiting until every migrating topic is at or below the lag threshold
  | 'threshold-met' // kcp checks the Gateway takes on changes, before fencing
  | 'fencing' // the fence is up; waiting for every migrating topic to reach zero lag
  | 'zero-lag' // every migrating topic is at zero lag, with the fence up
  | 'promoting' // promote sent: the mirrors are PENDING_STOPPED
  | 'promoted' // the mirrors are STOPPED
  | 'switching' // routed to the destination, fence removed
  | 'completed'; // this migration is done; select topics for the next one

/**
 * The dynamic route's rules: the topics a routing condition sends to the
 * destination (everything else goes to the source, the default), and the
 * topics a fencing rule blocks.
 */
export interface GatewayConfig {
  routed: number[];
  fenced: number[];
}

export function routesTo(config: GatewayConfig, topic: number): ClusterId {
  return config.routed.includes(topic) ? 'destination' : 'source';
}

/** Where the Gateway sends a topic's traffic under a config. */
export type TopicRoute = ClusterId | 'fenced';

export function routeOf(config: GatewayConfig, topic: number): TopicRoute {
  return config.fenced.includes(topic) ? 'fenced' : routesTo(config, topic);
}


export interface Topic {
  name: string;
  sourceEnd: number;
  destinationEnd: number;
  mirrorState: MirrorState;
  promoteTarget: number;
  promoteRequestedAt: number;
}

// Clients are only sources and sinks of messages: the model doesn't simulate
// client behaviour (buffering, retries, in-flight limits), so a
// fenced produce or fetch is rejected and nothing more is said about what the
// client does. Every message travels on its own, one dot each.
export interface Producer {
  id: number;
  app: number;
  topic: number;
  due: number; // the fraction of the next message produced so far, at this producer's rate
}

export interface Consumer {
  id: number;
  app: number;
  topic: number;
  position: number; // messages delivered so far
  requested: number; // messages fetched, including those still on the way back
  nextRejectShownAt: number;
}

// Every rate is per second on screen at the page's 1x (timing.second).
export interface Settings {
  producerRates: number[]; // messages per second, one per producer (producer i produces to topic i)
  linkLimit: number; // messages per second the cluster link copies at most, across every topic it mirrors
  lagThreshold: number; // policies.lagThreshold: per-topic lag tolerated before the fence
}

export const defaultSettings: Settings = {
  producerRates: [4, 4, 4, 4, 4, 4],
  linkLimit: 20,
  lagThreshold: 5,
};

// Scaled timings, in simulated milliseconds.
export const timing = {
  // One second on screen at the page's 1x. Every rate counts messages per this second.
  second: 250,
  // Fine enough that each message sets off on its own, even at the fastest rates.
  step: 10,
  hop: 450, // one network hop between boxes
  linkHop: 700,
  rejectShown: 250, // a fenced consumer's rejected fetch is drawn at most this often
  // Not a wait kcp makes: each kcp step after the lag check stays on screen at
  // least this long so a viewer can follow it. 1250 model ms is 5 s on screen
  // at the page's 1x. The lag check ends the moment the threshold is met.
  stageHold: 1250,
  // Not a wait kcp makes either: the pause between starting a migration and
  // the lag check, so the two show apart. 500 model ms is 2 s on screen at 1x.
  startHold: 500,
};

export type NodeId =
  | `app:${number}` // an application, connected to the route's one bootstrap endpoint
  | `gateway:${number}` // the Gateway's route line for a topic
  | `source:${number}`
  | `destination:${number}`;

// A Hop is one dot travelling between two boxes. The view animates it for
// `duration` simulated milliseconds.
export interface Hop {
  at: number; // model time the dot set off
  topic: number;
  from: NodeId;
  to: NodeId;
  duration: number;
  kind: 'produce' | 'reject' | 'fetch' | 'link';
}

export interface LogLine {
  at: number;
  text: string;
  tone: 'info' | 'ok' | 'warn';
}

type Pending =
  | { at: number; type: 'produce-at-gateway'; producer: number }
  | { at: number; type: 'produce-at-cluster'; producer: number; cluster: ClusterId }
  | { at: number; type: 'fetch-back-at-gateway'; consumer: number }
  | { at: number; type: 'fetch-delivered'; consumer: number };

/** The settings a single control changes. Producer rates change one producer at a time. */
export type LiveSetting = Exclude<keyof Settings, 'producerRates'>;

/**
 * Everything from outside that changes the model. A run is fully described by
 * its starting settings and its inputs with the model time of each, so any
 * moment can be rebuilt by replaying them.
 */
export type Input =
  | { type: 'start' }
  | { type: 'toggle-topic'; topic: number } // add a topic to the next migration, or take it out
  | { type: 'producer-rate'; producer: number; value: number }
  | { type: 'setting'; key: LiveSetting; value: number };

export const TOPIC_NAMES = ['orders', 'shipments', 'payments', 'inventory', 'users', 'events'];

// The topics were in use long before the migration, so each starts at its own
// offset, with its mirror in sync and its consumers caught up.
export const STARTING_OFFSETS = [48213, 20977, 35560, 9124, 3051, 71830];

/** An application produces to some topics and consumes from others, all through the route's one bootstrap endpoint. */
export interface Application {
  name: string;
  produces: number[];
  consumes: number[];
}

// Each topic has one producing application; some topics are read by several.
export const APPLICATIONS: Application[] = [
  { name: 'Checkout', produces: [0, 2], consumes: [3] },
  { name: 'Fulfilment', produces: [1], consumes: [0] },
  { name: 'Stock', produces: [3], consumes: [1] },
  { name: 'Accounts', produces: [4, 5], consumes: [] },
  { name: 'Analytics', produces: [], consumes: [0, 2, 4, 5] },
];

export class Simulation {
  now = 0;
  settings: Settings;
  phase: Phase = 'idle';
  topics: Topic[];
  producers: Producer[];
  consumers: Consumer[];
  /** The route rules the Gateway applies: the ones kcp last patched onto the Gateway CR. */
  gateway: GatewayConfig = { routed: [], fenced: [] };
  /** The topics this run is moving. */
  migrating: number[] = [];
  migrationNumber = 0;
  /** The topics selected for the next migration. The first migration starts with some selected. */
  selection: number[] = [0, 1];
  log: LogLine[] = [];
  /** When kcp last patched the fence onto the Gateway. */
  fencedAt = -Infinity;
  /** Every phase this run entered, with the model time it entered it. */
  phaseChanges: { at: number; phase: Phase; migration: number }[] = [];

  private pending: Pending[] = [];
  private hops: Hop[] = [];
  /** The fraction of the next message the link has copied so far. */
  private linkDue = 0;
  /** The topic the link copies from next: topics take turns. */
  private linkNext = 0;

  constructor(settings: Partial<Settings> = {}) {
    this.settings = structuredClone({ ...defaultSettings, ...settings });
    this.topics = TOPIC_NAMES.map((name, i) => ({
      name,
      sourceEnd: STARTING_OFFSETS[i],
      destinationEnd: STARTING_OFFSETS[i],
      mirrorState: 'ACTIVE',
      promoteTarget: 0,
      promoteRequestedAt: 0,
    }));
    this.producers = APPLICATIONS.flatMap((a, app) => a.produces.map((topic) => ({ app, topic })))
      .sort((x, y) => x.topic - y.topic)
      .map(({ app, topic }, id) => ({ id, app, topic, due: 0 }));
    this.consumers = APPLICATIONS.flatMap((a, app) => a.consumes.map((topic) => ({ app, topic }))).map(({ app, topic }, id) => ({
      id,
      app,
      topic,
      position: STARTING_OFFSETS[topic],
      requested: STARTING_OFFSETS[topic],
      nextRejectShownAt: 0,
    }));
  }

  // ---- public API -------------------------------------------------------

  lag(topic: Topic): number {
    if (topic.mirrorState === 'STOPPED') return 0;
    return topic.sourceEnd - topic.destinationEnd;
  }

  /** Apply an input at the current model time. */
  apply(input: Input): void {
    if (input.type === 'start') this.start();
    else if (input.type === 'toggle-topic') this.toggleTopic(input.topic);
    else if (input.type === 'producer-rate') this.settings.producerRates[input.producer] = input.value;
    else this.settings[input.key] = input.value;
  }

  /** Topics can be selected between migrations, from those not moved yet. */
  canPick(topic: number): boolean {
    return this.betweenRuns() && !this.gateway.routed.includes(topic);
  }

  betweenRuns(): boolean {
    return this.phase === 'idle' || this.phase === 'completed';
  }

  /** Every topic is routed to the destination. */
  allMoved(): boolean {
    return this.gateway.routed.length === this.topics.length;
  }

  private toggleTopic(topic: number): void {
    if (!this.canPick(topic)) return;
    this.selection = this.selection.includes(topic)
      ? this.selection.filter((t) => t !== topic)
      : [...this.selection, topic].sort((a, b) => a - b);
  }

  private names(topics: number[]): string {
    return topics.map((t) => this.topics[t].name).join(', ');
  }

  private migratingTopics(): Topic[] {
    return this.migrating.map((t) => this.topics[t]);
  }

  /** An independent copy: the same state, advancing the same way. */
  clone(): Simulation {
    const copy = Object.create(Simulation.prototype) as Simulation;
    return Object.assign(copy, structuredClone({ ...this }));
  }

  /** Run kcp for the selected topics: each migration is its own run. */
  start(): void {
    if (!this.betweenRuns() || this.selection.length === 0) return;
    this.migrationNumber++;
    this.migrating = this.selection;
    this.selection = [];
    this.phase = 'started';
    this.notePhase();
    this.say(`Migration ${this.migrationNumber}: ${this.names(this.migrating)}`, 'info');
  }

  /** Advance the model to `time` and return the hops that started on the way. */
  advanceTo(time: number): Hop[] {
    while (this.now + timing.step <= time) {
      this.now += timing.step;
      this.tick();
    }
    const out = this.hops;
    this.hops = [];
    return out;
  }

  // ---- the tick ---------------------------------------------------------

  private tick(): void {
    this.runPending();
    this.runProducers();
    this.runConsumers();
    this.runLink();
    this.runKcp();
    this.notePhase();
  }

  private notePhase(): void {
    const last = this.phaseChanges.at(-1);
    if (last?.phase !== this.phase || last.migration !== this.migrationNumber) {
      this.phaseChanges.push({ at: this.now, phase: this.phase, migration: this.migrationNumber });
    }
  }

  private schedule(p: Pending): void {
    this.pending.push(p);
  }

  private hop(topic: number, from: NodeId, to: NodeId, kind: Hop['kind'], duration = timing.hop): void {
    this.hops.push({ at: this.now, topic, from, to, kind, duration });
  }

  private say(text: string, tone: LogLine['tone']): void {
    this.log.push({ at: this.now, text, tone });
  }

  private runPending(): void {
    const due = this.pending.filter((p) => p.at <= this.now);
    if (due.length === 0) return;
    this.pending = this.pending.filter((p) => p.at > this.now);
    due.sort((a, b) => a.at - b.at);
    for (const p of due) this.handle(p);
  }

  private handle(p: Pending): void {
    switch (p.type) {
      case 'produce-at-gateway': {
        const producer = this.producers[p.producer];
        const route = routeOf(this.gateway, producer.topic);
        if (route === 'fenced') {
          // A fenced topic's produce is rejected: nothing is written.
          this.hop(producer.topic, `gateway:${producer.topic}`, `app:${producer.app}`, 'reject');
          return;
        }
        this.hop(producer.topic, `gateway:${producer.topic}`, `${route}:${producer.topic}`, 'produce');
        this.schedule({ at: this.now + timing.hop, type: 'produce-at-cluster', producer: p.producer, cluster: route });
        return;
      }
      case 'produce-at-cluster': {
        const producer = this.producers[p.producer];
        this.append(this.topics[producer.topic], p.cluster);
        return;
      }
      case 'fetch-back-at-gateway': {
        const consumer = this.consumers[p.consumer];
        this.hop(consumer.topic, `gateway:${consumer.topic}`, `app:${consumer.app}`, 'fetch');
        this.schedule({ at: this.now + timing.hop, type: 'fetch-delivered', consumer: p.consumer });
        return;
      }
      case 'fetch-delivered': {
        const consumer = this.consumers[p.consumer];
        consumer.position += 1;
        return;
      }
    }
  }

  private append(topic: Topic, cluster: ClusterId): void {
    if (cluster === 'source') topic.sourceEnd += 1;
    else topic.destinationEnd += 1;
  }

  // ---- the gateway ------------------------------------------------------

  /** Patch the Gateway CR. The Gateway takes on the change at once. */
  private applyConfig(change: Partial<GatewayConfig>, why: string): void {
    this.gateway = { ...structuredClone(this.gateway), ...change };
    this.say(`Patch the Gateway CR: ${why}`, 'info');
  }

  // ---- clients ----------------------------------------------------------

  /** Each producer sends each message the moment it is due at its own rate. */
  private runProducers(): void {
    for (const p of this.producers) {
      p.due += (this.settings.producerRates[p.id] * timing.step) / timing.second;
      for (; p.due >= 1; p.due -= 1) {
        this.hop(p.topic, `app:${p.app}`, `gateway:${p.topic}`, 'produce');
        this.schedule({ at: this.now + timing.hop, type: 'produce-at-gateway', producer: p.id });
      }
    }
  }

  /**
   * Each consumer reads each new message of its topic, through the Gateway, as
   * soon as there is one. A fenced topic's fetch is rejected: nothing is read.
   */
  private runConsumers(): void {
    for (const c of this.consumers) {
      const route = routeOf(this.gateway, c.topic);
      if (route === 'fenced') {
        if (this.now < c.nextRejectShownAt) continue;
        c.nextRejectShownAt = this.now + timing.rejectShown;
        this.hop(c.topic, `gateway:${c.topic}`, `app:${c.app}`, 'reject');
        continue;
      }
      const topic = this.topics[c.topic];
      const end = route === 'source' ? topic.sourceEnd : topic.destinationEnd;
      // One message a step, so messages that land together still travel apart.
      if (end <= c.requested) continue;
      c.requested += 1;
      this.hop(c.topic, `${route}:${c.topic}`, `gateway:${c.topic}`, 'fetch');
      this.schedule({ at: this.now + timing.hop, type: 'fetch-back-at-gateway', consumer: c.id });
    }
  }

  // ---- cluster link -----------------------------------------------------

  /**
   * The link copies one message at a time, up to its limit across every topic
   * it mirrors. Topics that are behind take turns, so a topic that needs less
   * than its share is kept up to date and the rest share what is left.
   */
  private runLink(): void {
    const behind = (t: Topic) => t.mirrorState !== 'STOPPED' && t.destinationEnd < t.sourceEnd;
    // Spare throughput doesn't build up while there is nothing to copy.
    this.linkDue = Math.min(
      this.linkDue + (this.settings.linkLimit * timing.step) / timing.second,
      this.topics.some(behind) ? Infinity : 1,
    );
    while (this.linkDue >= 1 && this.topics.some(behind)) {
      const i = this.linkNext;
      this.linkNext = (i + 1) % this.topics.length;
      const t = this.topics[i];
      if (!behind(t)) continue;
      t.destinationEnd += 1;
      this.linkDue -= 1;
      this.hop(i, `source:${i}`, `destination:${i}`, 'link', timing.linkHop);
    }
  }

  // ---- kcp --------------------------------------------------------------

  /** kcp reads lag from both clusters on every model step, so it moves on the moment a condition is met. */
  private runKcp(): void {
    // The waits for a condition end the moment it is met. Every other step is
    // held long enough to follow (timing.stageHold).
    const held = !this.betweenRuns() && this.phase !== 'lag-check' && this.phase !== 'fencing';
    const hold = this.phase === 'started' ? timing.startHold : timing.stageHold;
    if (held && this.now - (this.phaseChanges.at(-1)?.at ?? 0) < hold) return;
    switch (this.phase) {
      case 'started': {
        this.say(`Waiting until the lag of every topic in this migration is at or below ${this.settings.lagThreshold} (lagThreshold)`, 'info');
        this.phase = 'lag-check';
        return;
      }
      case 'lag-check': {
        const threshold = this.settings.lagThreshold;
        if (this.migratingTopics().every((t) => this.lag(t) <= threshold)) {
          this.say(`Every topic in this migration is at or below the lag threshold (${threshold})`, 'ok');
          // Before anything blocks traffic, kcp checks the Gateway takes on
          // config changes, once per run. The log leaves that detail out.
          this.phase = 'threshold-met';
        }
        return;
      }
      case 'threshold-met': {
        this.fence();
        return;
      }
      case 'fencing': {
        // Whatever lagThreshold allowed, kcp promotes only at exactly zero lag,
        // with the fence up and nothing for these topics still landing on the source.
        if (this.migratingTopics().every((t) => this.lag(t) === 0) && !this.migrating.some((t) => this.landingOnSource(t))) {
          this.say(`Zero lag on every topic in this migration: ${this.names(this.migrating)}`, 'ok');
          this.phase = 'zero-lag';
        }
        return;
      }
      case 'zero-lag': {
        for (const t of this.migratingTopics()) {
          t.mirrorState = 'PENDING_STOPPED';
          t.promoteTarget = t.sourceEnd;
          t.promoteRequestedAt = this.now;
        }
        this.say(`Promote ${this.names(this.migrating)} on the cluster link (point of no return)`, 'warn');
        this.phase = 'promoting';
        return;
      }
      case 'promoting': {
        this.settlePromotes();
        if (this.migratingTopics().every((t) => t.mirrorState === 'STOPPED')) {
          this.say('Every mirror topic in this migration is STOPPED', 'ok');
          this.phase = 'promoted';
        }
        return;
      }
      case 'promoted': {
        // One rules update: route this migration's topics to the destination and remove their fence.
        this.applyConfig(
          {
            routed: [...this.gateway.routed, ...this.migrating].sort((a, b) => a - b),
            fenced: this.gateway.fenced.filter((t) => !this.migrating.includes(t)),
          },
          `route ${this.names(this.migrating)} to the destination and remove their fencing rule`,
        );
        this.phase = 'switching';
        return;
      }
      case 'switching': {
        this.say(`Migration ${this.migrationNumber} completed: ${this.names(this.migrating)} now flow to the destination`, 'ok');
        this.phase = 'completed';
        if (this.allMoved()) this.say('Every topic is on the destination', 'ok');
        return;
      }
    }
  }

  private fence(): void {
    this.applyConfig(
      { fenced: [...this.gateway.fenced, ...this.migrating].sort((a, b) => a - b) },
      `add a fencing rule for ${this.names(this.migrating)}`,
    );
    this.fencedAt = this.now;
    this.phase = 'fencing';
  }

  /** A message for this topic got past the Gateway before the fence and is still on its way to the source. */
  private landingOnSource(topic: number): boolean {
    return this.pending.some((p) => p.type === 'produce-at-cluster' && p.cluster === 'source' && this.producers[p.producer].topic === topic);
  }

  /** A promoted mirror stops as soon as it has copied everything up to the promote. */
  private settlePromotes(): void {
    for (const t of this.topics) {
      if (t.mirrorState === 'PENDING_STOPPED' && t.destinationEnd >= t.promoteTarget) t.mirrorState = 'STOPPED';
    }
  }
}
