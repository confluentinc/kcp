import { describe, expect, it } from 'vitest';
import { APPLICATIONS, STARTING_OFFSETS, Simulation, TOPIC_NAMES, timing, type Settings } from './model';

const ORDERS = 0;
const SHIPMENTS = 1;
const PAYMENTS = 2;
const INVENTORY = 3;
const USERS = 4;

// The page starts with a link slower than the producers, so lag builds. Most
// tests follow a migration through, so they start with a link that keeps up.
const LINK_KEEPS_UP: Partial<Settings> = { linkLimit: 40 };

function build(settings: Partial<Settings>): Simulation {
  return new Simulation({ ...LINK_KEEPS_UP, ...settings });
}

/** Run a migration of the selected topics from Start until it completes. */
function runMigration(sim: Simulation, limitMs = 180_000): void {
  if (sim.now < 5_000) sim.advanceTo(5_000); // traffic flowing first
  sim.start();
  const until = sim.now + limitMs;
  let t = sim.now;
  while (t < until && !sim.betweenRuns()) sim.advanceTo((t += 250));
}

function pick(sim: Simulation, topics: number[]): void {
  for (const t of sim.selection) sim.apply({ type: 'toggle-topic', topic: t });
  for (const t of topics) sim.apply({ type: 'toggle-topic', topic: t });
}

describe('traffic', () => {
  it('each dot is one message: a producer sends as many dots a second as its rate', () => {
    const sim = build({ producerRates: [1, 10, 5, 5, 5, 5] });
    sim.advanceTo(timing.second); // warm up
    const sent = TOPIC_NAMES.map(() => 0);
    const seconds = 10;
    for (let s = 2; s <= seconds + 1; s++) {
      for (const h of sim.advanceTo(s * timing.second)) if (h.kind === 'produce' && h.from.startsWith('app:')) sent[h.topic]++;
    }
    expect(sent[ORDERS]).toBe(1 * seconds);
    expect(sent[SHIPMENTS]).toBe(10 * seconds);
  });

  it('messages that set off together still travel apart: no two dots share a path and a start', () => {
    const sim = build({ producerRates: Array(6).fill(10) });
    const seen = new Map<string, number>();
    for (const h of sim.advanceTo(5 * timing.second)) {
      if (h.kind === 'reject') continue;
      const key = `${h.from}>${h.to}@${h.at}`;
      seen.set(key, (seen.get(key) ?? 0) + 1);
      // Only the same message read by several consumers leaves its cluster together, one read each.
      const readers = h.kind === 'fetch' && !h.to.startsWith('app:') ? sim.consumers.filter((c) => c.topic === h.topic).length : 1;
      expect(seen.get(key)).toBeLessThanOrEqual(readers);
    }
  });

  it('each consumer reads every message of its topic, one dot each, and keeps up', () => {
    const sim = build({});
    sim.advanceTo(5_000);
    const produced = TOPIC_NAMES.map(() => 0);
    const read = sim.consumers.map(() => 0);
    for (let t = 5_000; t <= 15_000; t += 250) {
      for (const h of sim.advanceTo(t)) {
        if (h.kind === 'produce' && h.from.startsWith('app:')) produced[h.topic]++;
        if (h.kind === 'fetch' && h.to.startsWith('app:')) {
          const app = Number(h.to.split(':')[1]);
          read[sim.consumers.findIndex((c) => c.app === app && c.topic === h.topic)]++;
        }
      }
    }
    sim.consumers.forEach((c, i) => {
      // What is still on its way: two hops each way, at the producer's rate.
      const onTheWay = (sim.settings.producerRates[c.topic] * 4 * 450) / timing.second + 2;
      expect(Math.abs(read[i] - produced[c.topic])).toBeLessThanOrEqual(onTheWay);
      expect(sim.topics[c.topic].sourceEnd - c.requested).toBeLessThanOrEqual(1);
    });
  });

  it('starts with every producer at 4 msg/s, a 20 msg/s link and a lag threshold of 5, so lag builds from the start', () => {
    const sim = new Simulation();
    expect(sim.settings.producerRates).toEqual([4, 4, 4, 4, 4, 4]);
    expect(sim.settings.linkLimit).toBe(20);
    expect(sim.settings.lagThreshold).toBe(5);
    sim.advanceTo(20 * timing.second);
    for (const t of sim.topics) expect(sim.lag(t)).toBeGreaterThan(5);
  });

  it('lag grows when the link is slower than the producers', () => {
    const sim = build({ producerRates: Array(6).fill(8), linkLimit: 24 });
    sim.advanceTo(10_000);
    for (const t of sim.topics) expect(sim.lag(t)).toBeGreaterThan(10);
  });

  it('each topic starts at its own offset, in sync on both clusters, and grows at its producer\'s rate', () => {
    const sim = build({});
    expect(new Set(STARTING_OFFSETS).size).toBe(TOPIC_NAMES.length);
    sim.topics.forEach((t, i) => {
      expect(t.sourceEnd).toBe(STARTING_OFFSETS[i]);
      expect(sim.lag(t)).toBe(0);
    });
    const seconds = 20;
    sim.advanceTo(seconds * timing.second);
    sim.topics.forEach((t, i) => {
      // 20 s at the producer's rate, less what is still on its way (two hops).
      const grown = t.sourceEnd - STARTING_OFFSETS[i];
      const rate = sim.settings.producerRates[i];
      expect(grown).toBeGreaterThanOrEqual(rate * seconds - (rate * 2 * 450) / timing.second - 1);
      expect(grown).toBeLessThanOrEqual(rate * seconds);
    });
  });

  it('the link limit is shared by every topic: 60 msg/s produced through a 20 msg/s limit lags on every topic', () => {
    const sim = build({ producerRates: Array(6).fill(10), linkLimit: 20 });
    // Until the first messages reach the source, the link has nothing to copy.
    const warmUp = 10;
    sim.advanceTo(warmUp * timing.second);
    const seconds = 20;
    let copied = 0;
    for (let s = 1; s <= seconds; s++) copied += sim.advanceTo((warmUp + s) * timing.second).filter((h) => h.kind === 'link').length;
    expect(copied).toBe(20 * seconds);
    for (const t of sim.topics) expect(sim.lag(t)).toBeGreaterThan(100);
  });

  it('a producer\'s rate is its own: a fast producer builds lag on its topic only', () => {
    // The others need 20 msg/s and get it; users gets the 6 left over, less than its 10.
    const sim = build({ linkLimit: 26 });
    sim.advanceTo(2_000);
    sim.apply({ type: 'producer-rate', producer: USERS, value: 10 });
    sim.advanceTo(20_000);
    expect(sim.lag(sim.topics[USERS])).toBeGreaterThan(40);
    for (const t of sim.topics.filter((_, i) => i !== USERS)) expect(sim.lag(t)).toBeLessThan(10);
  });
});

describe('applications', () => {
  it('every topic has exactly one producing application, and at least one consuming one', () => {
    TOPIC_NAMES.forEach((_, t) => {
      expect(APPLICATIONS.filter((a) => a.produces.includes(t))).toHaveLength(1);
      expect(APPLICATIONS.filter((a) => a.consumes.includes(t)).length).toBeGreaterThan(0);
    });
    const sim = build({});
    expect(sim.producers.map((p) => p.topic)).toEqual([0, 1, 2, 3, 4, 5]);
  });

  it('after a migration, one application writes to both clusters through the same route', () => {
    const sim = build({});
    runMigration(sim); // orders and shipments move
    const checkout = APPLICATIONS.findIndex((a) => a.name === 'Checkout');
    const wrote = new Set<string>();
    const until = sim.now + 5_000;
    for (let t = sim.now; t < until; t += 250) {
      for (const h of sim.advanceTo(t)) if (h.kind === 'produce' && h.from.startsWith('gateway:')) wrote.add(`${h.topic}>${h.to.split(':')[0]}`);
    }
    // Checkout produces orders (moved) and payments (not moved).
    expect(APPLICATIONS[checkout].produces).toEqual([ORDERS, PAYMENTS]);
    expect(wrote).toContain(`${ORDERS}>destination`);
    expect(wrote).toContain(`${PAYMENTS}>source`);
  });
});

describe('selecting topics', () => {
  it('starts with topics selected for the first migration, so Start just works', () => {
    const sim = build({});
    expect(sim.selection).toEqual([ORDERS, SHIPMENTS]);
  });

  it('a viewer adds and removes topics between runs, but not during one', () => {
    const sim = build({});
    pick(sim, [PAYMENTS]);
    expect(sim.selection).toEqual([PAYMENTS]);
    sim.advanceTo(5_000);
    sim.start();
    sim.apply({ type: 'toggle-topic', topic: USERS });
    expect(sim.selection).toEqual([]);
  });

  it('a moved topic cannot be picked again', () => {
    const sim = build({});
    runMigration(sim);
    expect(sim.phase).toBe('completed');
    sim.apply({ type: 'toggle-topic', topic: ORDERS });
    expect(sim.selection).toEqual([]);
  });

  it('Start does nothing with no topics picked', () => {
    const sim = build({});
    pick(sim, []);
    sim.start();
    expect(sim.phase).toBe('idle');
  });
});

describe('a migration', () => {
  it('moves only its topics: they end routed to the destination, the rest stay on the source', () => {
    const sim = build({});
    runMigration(sim);
    expect(sim.phase).toBe('completed');
    expect(sim.topics.map((t) => t.mirrorState)).toEqual(['STOPPED', 'STOPPED', 'ACTIVE', 'ACTIVE', 'ACTIVE', 'ACTIVE']);
    expect(sim.gateway.routed).toEqual([ORDERS, SHIPMENTS]);
    expect(sim.gateway.fenced).toEqual([]);
  });

  it('fences only its topics, and every other topic keeps flowing to the source meanwhile', () => {
    const sim = build({});
    sim.advanceTo(5_000);
    sim.start();
    const before = sim.topics[USERS].sourceEnd;
    let t = sim.now;
    let fencedSeen = false;
    let otherRejected = 0;
    while (sim.phase !== 'completed' && t < 120_000) {
      for (const h of sim.advanceTo((t += 50))) {
        if (h.kind === 'reject' && !h.from.endsWith(':0') && !h.from.endsWith(':1')) otherRejected++;
      }
      if (sim.gateway.fenced.join() === '0,1') fencedSeen = true;
    }
    expect(fencedSeen).toBe(true);
    expect(otherRejected).toBe(0);
    expect(sim.topics[USERS].sourceEnd).toBeGreaterThan(before);
  });

  it('a fenced produce is rejected and nothing is written, so a migrating topic\'s source log stops at the fence', () => {
    const sim = build({});
    sim.advanceTo(5_000);
    sim.start();
    let t = sim.now;
    while (sim.gateway.fenced.length === 0 && t < 120_000) sim.advanceTo((t += 50));
    // Let what was already past the Gateway land.
    sim.advanceTo((t += 1_000));
    const stoppedAt = sim.topics[ORDERS].sourceEnd;
    let rejected = 0;
    while (sim.gateway.fenced.includes(ORDERS) && t < 120_000) {
      for (const h of sim.advanceTo((t += 50))) if (h.kind === 'reject' && h.topic === ORDERS) rejected++;
      if (sim.gateway.fenced.includes(ORDERS)) expect(sim.topics[ORDERS].sourceEnd).toBe(stoppedAt);
    }
    expect(rejected).toBeGreaterThan(0);
  });

  it('holds every step but the two waits on screen for at least the stage hold, so a viewer can follow it', () => {
    const sim = build({});
    runMigration(sim);
    const changes = sim.phaseChanges.filter((c) => c.migration === 1);
    for (let i = 0; i + 1 < changes.length; i++) {
      // The lag check and the wait for zero lag end the moment their condition is met.
      if (changes[i].phase === 'lag-check' || changes[i].phase === 'fencing') continue;
      const hold = changes[i].phase === 'started' ? timing.startHold : timing.stageHold;
      expect(changes[i + 1].at - changes[i].at).toBeGreaterThanOrEqual(hold);
    }
  });

  it('starts the lag check 2 s on screen after the migration starts', () => {
    const sim = build({});
    sim.advanceTo(5 * timing.second);
    sim.start();
    expect(sim.phase).toBe('started');
    let t = sim.now;
    while (sim.phase === 'started' && t < 60_000) sim.advanceTo((t += timing.step));
    const [started, lagCheck] = sim.phaseChanges.filter((c) => c.migration === 1);
    expect(started.phase).toBe('started');
    expect(lagCheck.phase).toBe('lag-check');
    expect(lagCheck.at - started.at).toBe(2 * timing.second);
    expect(sim.log.at(-1)?.text).toMatch(/^Waiting until the lag of every topic/);
  });

  it('each step starts at the change it is named for', () => {
    // A slow link, so the migrating topics are behind when the fence goes up.
    const sim = build({ linkLimit: 22, lagThreshold: 40 });
    sim.advanceTo(10 * timing.second);
    sim.start();
    let t = sim.now;
    const migrating = () => [ORDERS, SHIPMENTS].map((i) => sim.topics[i]);
    const entered: string[] = [];
    let zeroSince = -1; // since when every migrating topic has been at zero lag
    while (!sim.betweenRuns() && t < 300_000) {
      const before = sim.phase;
      sim.advanceTo((t += timing.step));
      if (migrating().every((x) => sim.lag(x) === 0)) {
        if (zeroSince < 0) zeroSince = sim.now;
      } else zeroSince = -1;
      if (sim.phase === before) continue;
      entered.push(sim.phase);
      switch (sim.phase) {
        case 'lag-check':
          // 2 s on screen after the migration started, with nothing changed yet.
          expect(sim.now - sim.phaseChanges.find((c) => c.migration === 1 && c.phase === 'started')!.at).toBe(2 * timing.second);
          expect(sim.gateway.fenced).toEqual([]);
          break;
        case 'threshold-met':
          for (const x of migrating()) expect(sim.lag(x)).toBeLessThanOrEqual(40);
          break;
        case 'fencing':
          expect(sim.gateway.fenced).toEqual([ORDERS, SHIPMENTS]);
          expect(Math.max(...migrating().map((x) => sim.lag(x)))).toBeGreaterThan(0);
          break;
        case 'zero-lag':
          // Marked when zero lag is reached: at most a hop later, for messages already past the Gateway to land.
          expect(zeroSince).toBeGreaterThan(0);
          expect(sim.now - zeroSince).toBeLessThanOrEqual(timing.hop + timing.step);
          expect(migrating().map((x) => x.mirrorState)).toEqual(['ACTIVE', 'ACTIVE']);
          break;
        case 'promoting':
          expect(migrating().map((x) => x.mirrorState)).toEqual(['PENDING_STOPPED', 'PENDING_STOPPED']);
          break;
        case 'promoted':
          expect(migrating().map((x) => x.mirrorState)).toEqual(['STOPPED', 'STOPPED']);
          expect(sim.gateway.routed).toEqual([]);
          break;
        case 'switching':
          expect(sim.gateway.routed).toEqual([ORDERS, SHIPMENTS]);
          expect(sim.gateway.fenced).toEqual([]);
          break;
      }
    }
    expect(entered).toEqual(['lag-check', 'threshold-met', 'fencing', 'zero-lag', 'promoting', 'promoted', 'switching', 'completed']);
  });

  it('meets the threshold the first model step every migrating topic is at or below it', () => {
    // The link falls behind, so the lag check waits; then the link catches up.
    const sim = build({ linkLimit: 10, lagThreshold: 20 });
    sim.advanceTo(20 * timing.second);
    sim.start();
    let t = sim.now;
    sim.apply({ type: 'setting', key: 'linkLimit', value: 80 });
    let firstAtOrBelow = -1; // the first step of the lag check with every migrating topic at or below the threshold
    while ((sim.phase === 'started' || sim.phase === 'lag-check') && t < 120_000) {
      sim.advanceTo((t += timing.step));
      const met = [ORDERS, SHIPMENTS].every((i) => sim.lag(sim.topics[i]) <= 20);
      if (met && sim.phase !== 'started' && firstAtOrBelow < 0) firstAtOrBelow = sim.now;
    }
    const metAt = sim.phaseChanges.find((c) => c.phase === 'threshold-met')!.at;
    expect(firstAtOrBelow).toBeGreaterThan(0);
    expect(metAt - firstAtOrBelow).toBeLessThanOrEqual(timing.step);
  });

  it('runs migration after migration until every topic is on the destination', () => {
    const sim = build({});
    runMigration(sim);
    pick(sim, [PAYMENTS, INVENTORY]);
    runMigration(sim);
    pick(sim, [USERS, 5]);
    runMigration(sim);
    expect(sim.migrationNumber).toBe(3);
    expect(sim.allMoved()).toBe(true);
  });

  it('the destination carries the log on from where the source stopped', () => {
    const sim = build({});
    runMigration(sim);
    sim.advanceTo(sim.now + 10_000);
    for (const t of [ORDERS, SHIPMENTS]) expect(sim.topics[t].destinationEnd).toBeGreaterThan(sim.topics[t].promoteTarget);
  });

  it('waits at the lag check while the migrating topics\' lag stays above the threshold', () => {
    const sim = build({ producerRates: Array(6).fill(8), linkLimit: 24, lagThreshold: 0 });
    runMigration(sim, 60_000);
    expect(sim.phase).toBe('lag-check');
    expect(sim.gateway.fenced).toEqual([]);
  });

  it('fences with lag up to the threshold, then promotes each migrating topic only at zero lag', () => {
    // The migrating topics fall behind by 1 message a second, so its lag is under 40 when the lag check starts.
    const sim = build({ producerRates: Array(6).fill(5), linkLimit: 24, lagThreshold: 40 });
    sim.advanceTo(5_000);
    sim.start();
    let t = sim.now;
    while ((sim.phase === 'started' || sim.phase === 'lag-check') && t < 120_000) sim.advanceTo((t += 50));
    const lagAtFence = [ORDERS, SHIPMENTS].map((i) => sim.lag(sim.topics[i]));
    expect(Math.max(...lagAtFence)).toBeGreaterThan(0);
    expect(Math.max(...lagAtFence)).toBeLessThanOrEqual(40);
    while (sim.phase !== 'completed' && t < 120_000) {
      sim.advanceTo((t += 50));
      for (const x of sim.topics) {
        if (x.mirrorState === 'PENDING_STOPPED' && x.promoteRequestedAt === sim.now) expect(x.destinationEnd).toBe(x.sourceEnd);
      }
    }
    expect(sim.phase).toBe('completed');
  });
});

describe('gateway config changes', () => {
  /** Step a migration from Start and call `see` after every model step until the fence is up. */
  function watchFence(sim: Simulation, see: () => void): void {
    sim.advanceTo(5_000);
    sim.start();
    let t = sim.now;
    while (sim.phase !== 'zero-lag' && t < 120_000) {
      sim.advanceTo((t += 50));
      see();
    }
  }

  it('each run checks the Gateway takes on changes once, when the threshold is met', () => {
    const sim = build({});
    runMigration(sim);
    pick(sim, [PAYMENTS]);
    runMigration(sim);
    const proofs = sim.phaseChanges.filter((c) => c.phase === 'threshold-met').map((c) => c.migration);
    expect(proofs).toEqual([1, 2]);
  });

  it('the fence goes up on all the migrating topics at once and stays up until the switch', () => {
    const sim = build({});
    let fencedAt = -1;
    watchFence(sim, () => {
      const fenced = sim.gateway.fenced;
      if (fenced.length > 0 && fencedAt < 0) {
        fencedAt = sim.now;
        expect(fenced).toEqual([ORDERS, SHIPMENTS]);
      }
    });
    expect(fencedAt).toBeGreaterThan(0);
    let t = sim.now;
    while (sim.phase !== 'switching' && t < 120_000) {
      expect(sim.gateway.fenced).toEqual([ORDERS, SHIPMENTS]);
      sim.advanceTo((t += timing.step));
    }
    expect(sim.gateway.fenced).toEqual([]);
  });
});

it('names six topics', () => {
  expect(TOPIC_NAMES).toHaveLength(6);
});
