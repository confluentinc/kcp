import { APPLICATIONS, routeOf, timing, type Hop, type NodeId, type Simulation, type TopicRoute } from '../sim/model';

// One colour per topic, for its boxes, wires and moving dots. Red, grey and
// blue stay for fenced, source and destination.
export const TOPIC_COLOURS = ['#e8743b', '#3c9d5d', '#b8860b', '#2b9aa0', '#8c6d4f', '#d4609c'];

const NS = 'http://www.w3.org/2000/svg';

type Attrs = Record<string, string | number>;

function el<K extends keyof SVGElementTagNameMap>(tag: K, attrs: Attrs = {}, parent?: Element): SVGElementTagNameMap[K] {
  const node = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v));
  parent?.appendChild(node);
  return node;
}

function text(parent: Element, x: number, y: number, cls: string, content = '', anchor = 'start'): SVGTextElement {
  const t = el('text', { x, y, class: cls, 'text-anchor': anchor }, parent);
  t.textContent = content;
  return t;
}

interface Point { x: number; y: number }

// ---- layout ---------------------------------------------------------------

const TOPICS = 6;

const GATEWAY = { x: 350, y: 150, w: 320, h: 700 };
// The Gateway's route table: one line per topic.
const GW_ROW = { x: 480, w: 170, h: 46 };
const routeY = (topic: number): number => 265 + topic * 104;

// Applications, stacked down the left and centred on the route table. Each
// lists the topics it produces to and consumes from, one line each, and each
// line has its own wire to that topic's row in the Gateway. A line it produces
// to carries a slider for that producer's rate.
const APP = { x: 16, w: 280, title: 30, line: 20, gap: 26 };
const RATE = { x: APP.x + 142, w: 78, h: 16 };
const appTopics = (app: number): number[] => [...APPLICATIONS[app].produces, ...APPLICATIONS[app].consumes];
const appH = (app: number): number => APP.title + appTopics(app).length * APP.line + 6;
const APP_TOP = (() => {
  const total = APPLICATIONS.reduce((sum, _, a) => sum + appH(a), 0) + APP.gap * (APPLICATIONS.length - 1);
  return (routeY(0) + routeY(TOPICS - 1)) / 2 - total / 2;
})();
const appTop = (app: number): number => {
  let y = APP_TOP;
  for (let a = 0; a < app; a++) y += appH(a) + APP.gap;
  return y;
};
// No application both produces to and consumes from the same topic, so an application and a topic name one line.
const appLineY = (app: number, topic: number): number =>
  appTop(app) + APP.title + appTopics(app).indexOf(topic) * APP.line + APP.line / 2;
const appPort = (app: number, topic: number): Point => ({ x: APP.x + APP.w, y: appLineY(app, topic) });
// Where an application's wire meets a topic's row: wires into the same row are spread down its edge.
const rowPort = (app: number, topic: number): Point => {
  const apps = APPLICATIONS.map((_, a) => a).filter((a) => appTopics(a).includes(topic));
  const k = apps.indexOf(app);
  return { x: GW_ROW.x, y: routeY(topic) + (k - (apps.length - 1) / 2) * 9 };
};

// Close to the Gateway, leaving room beside the drawing for the migration steps.
const CLUSTER = { x: 770, w: 300 };
const SOURCE = { y: 150, h: 330 };
const DEST = { y: 500, h: 340 };
const ROW = { x: CLUSTER.x + 18, w: 264, h: 40 };
const sourceRowY = (topic: number): number => SOURCE.y + 72 + topic * 44;
// Mirrors are drawn in the reverse order of their source topics, so the
// cluster link lanes nest instead of crossing.
const destRowY = (topic: number): number => DEST.y + 72 + (TOPICS - 1 - topic) * 44;
// Each topic row draws the tail of its log, one cell per offset, and its offset (the end of the log) beside it.
const CELLS = 40;
const OFFSET_W = 76;
const CELL = { x: ROW.x + 12, w: (ROW.w - 20 - OFFSET_W) / CELLS, h: 10 };
// Tall enough to leave the readout at its foot the same margin as at its sides.
const LINK = { x: CLUSTER.x + CLUSTER.w + 20, y: SOURCE.y, w: 180, h: 760, lane: 20 };
// kcp, below everything, wired to the bottom of the Gateway and of the cluster link.
// It holds the topics to migrate, the Start migration button and the lag threshold.
const KCP = { x: 650, y: 868, w: 420, h: 178 };
const KCP_WIRE_Y = KCP.y + KCP.h / 2;

const MAX_DOTS = 2000;

function curveTo(a: Point, b: Point): string {
  const mx = (a.x + b.x) / 2;
  return `C ${mx} ${a.y}, ${mx} ${b.y}, ${b.x} ${b.y}`;
}

function curve(a: Point, b: Point): string {
  return `M ${a.x} ${a.y} ${curveTo(a, b)}`;
}

function parse(id: NodeId): { kind: string; index: number } {
  const [kind, index] = id.split(':');
  return { kind, index: Number(index ?? 0) };
}

/** A moving dot: one message on its way along a path, placed each frame from the model's clock. */
interface Dot {
  hop: Hop;
  el: SVGCircleElement;
  points: Point[];
}

interface RouteRowEls {
  g: SVGGElement;
  route: SVGTextElement;
  // The topic's own lines from the Gateway to its topic on each cluster.
  toSource: SVGPathElement;
  toDest: SVGPathElement;
}

/** One line in an application's box: a topic it produces to or consumes from. */
interface AppLineEls {
  g: SVGGElement;
  topic: number;
  // A producer's line: its rate slider and the rate it shows.
  rate?: { input: HTMLInputElement; value: SVGTextElement };
}

export interface SceneHandlers {
  /** A viewer ticked or unticked a topic in the kcp box, to select it or take it out. */
  pick: (topic: number) => void;
  /** A viewer pressed Start migration in the kcp box. */
  start: () => void;
  /** A viewer moved a producer's rate slider. */
  rate: (producer: number, value: number) => void;
  /** A viewer moved the cluster link's throughput limit slider. */
  linkLimit: (value: number) => void;
  /** A viewer typed a lag threshold into the kcp box. */
  lagThreshold: (value: number) => void;
}

/** A coloured bar on a box's left edge marking its topic. */
function swatch(parent: Element, x: number, y: number, h: number, topic: number): void {
  el('rect', { x, y: y - h / 2, width: 5, height: h, rx: 2, fill: TOPIC_COLOURS[topic] }, parent);
}

/** A wire in its topic's colour, set inline so the wire rules' grey doesn't override it. */
function topicStyle(topic: number): string {
  return `stroke: ${TOPIC_COLOURS[topic]}`;
}

// ---- scene ----------------------------------------------------------------

export class Scene {
  private root: SVGSVGElement;
  private motionLayer: SVGGElement;
  private dotLayer: SVGGElement;
  // Points along each path a dot can take, sampled once, so placing a dot is a lookup.
  private motionPoints = new Map<string, Point[]>();
  private dots: Dot[] = [];

  private selectionTitle!: SVGTextElement;
  private topicBoxes: { label: HTMLLabelElement; box: HTMLInputElement }[] = [];
  private startButton!: HTMLButtonElement;
  private kcpAction!: SVGTextElement;
  private kcpDetail!: SVGTextElement;
  private lagThresholdInput!: HTMLInputElement;
  private kcpWires!: { gateway: SVGPathElement; link: SVGPathElement };
  private routeRows: RouteRowEls[] = [];
  private appLines: AppLineEls[] = [];
  private sourceRows: { g: SVGGElement; state: SVGTextElement; offset: SVGTextElement; cells: SVGRectElement[] }[] = [];
  private destRows: { pill: SVGTextElement; lag: SVGTextElement; offset: SVGTextElement; cells: SVGRectElement[] }[] = [];
  private linkLagText!: SVGTextElement;
  private linkLimitText!: SVGTextElement;
  private linkThresholdText!: SVGTextElement;
  private linkLimitInput!: HTMLInputElement;

  constructor(root: SVGSVGElement, private readonly layout: Simulation, private readonly handlers: SceneHandlers) {
    this.root = root;
    root.replaceChildren();
    const boxes = el('g', {}, root);
    // Wires that run into a box, drawn above the boxes so they reach their end.
    const over = el('g', {}, root);
    this.motionLayer = el('g', {}, root);
    this.dotLayer = el('g', {}, root);
    this.buildKcp(boxes);
    this.buildGateway(boxes, over);
    this.buildApps(boxes, over);
    this.buildClusters(boxes);
  }

  // ---- build ------------------------------------------------------------

  // kcp makes its changes to the Gateway (patching its route rules) and to the
  // cluster link (promoting mirror topics). Its wire to each lights up while
  // it is making one.
  private buildKcp(parent: SVGGElement): void {
    const r = 10;
    const gx = GATEWAY.x + GATEWAY.w / 2;
    const gy = GATEWAY.y + GATEWAY.h;
    const lx = LINK.x + LINK.w / 2;
    const ly = LINK.y + LINK.h;
    const gateway = el('path', { class: 'kcp-wire', d: `M ${gx} ${gy} V ${KCP_WIRE_Y - r} Q ${gx} ${KCP_WIRE_Y}, ${gx + r} ${KCP_WIRE_Y} H ${KCP.x}` }, parent);
    const link = el('path', { class: 'kcp-wire', d: `M ${lx} ${ly} V ${KCP_WIRE_Y - r} Q ${lx} ${KCP_WIRE_Y}, ${lx - r} ${KCP_WIRE_Y} H ${KCP.x + KCP.w}` }, parent);
    text(parent, gx + 18, KCP_WIRE_Y - 8, 'kcp-wire-label', 'patches route rules');
    text(parent, lx - 18, KCP_WIRE_Y - 8, 'kcp-wire-label', 'promotes', 'end');
    this.kcpWires = { gateway, link };
    el('rect', { x: KCP.x, y: KCP.y, width: KCP.w, height: KCP.h, rx: 8, class: 'box kcp-box' }, parent);
    text(parent, KCP.x + 16, KCP.y + 22, 'box-title', 'kcp');
    this.kcpAction = text(parent, KCP.x + 52, KCP.y + 22, 'kcp-action', '');
    this.kcpDetail = text(parent, KCP.x + 16, KCP.y + 42, 'value', '');

    // The topics to migrate: one checkbox per topic, ticked to select it.
    el('line', { x1: KCP.x, x2: KCP.x + KCP.w, y1: KCP.y + 54, y2: KCP.y + 54, class: 'kcp-divider' }, parent);
    this.selectionTitle = text(parent, KCP.x + 16, KCP.y + 74, 'box-sub', '');
    const topics = el('foreignObject', { x: KCP.x + 12, y: KCP.y + 82, width: KCP.w - 24, height: 52, class: 'topic-picker' }, parent);
    const list = document.createElement('div');
    this.layout.topics.forEach((t, i) => {
      const label = document.createElement('label');
      label.className = 'topic-choice';
      label.style.setProperty('--topic', TOPIC_COLOURS[i]);
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.addEventListener('change', () => this.handlers.pick(i));
      label.append(box, t.name);
      list.appendChild(label);
      this.topicBoxes.push({ label, box });
    });
    topics.appendChild(list);

    // Start migration, and kcp's setting: the lag threshold, typed in.
    el('line', { x1: KCP.x, x2: KCP.x + KCP.w, y1: KCP.y + 140, y2: KCP.y + 140, class: 'kcp-divider' }, parent);
    const y = KCP.y + 159;
    const start = el('foreignObject', { x: KCP.x + 14, y: y - 13, width: 150, height: 26, class: 'kcp-start' }, parent);
    this.startButton = document.createElement('button');
    this.startButton.className = 'primary';
    this.startButton.textContent = 'Start migration';
    this.startButton.addEventListener('click', () => this.handlers.start());
    start.appendChild(this.startButton);
    text(parent, KCP.x + KCP.w - 80, y + 4, 'kcp-setting', 'Lag threshold', 'end');
    const holder = el('foreignObject', { x: KCP.x + KCP.w - 72, y: y - 11, width: 56, height: 22, class: 'number-box' }, parent);
    const input = document.createElement('input');
    Object.assign(input, { type: 'text', inputMode: 'numeric', maxLength: 5, autocomplete: 'off', value: String(this.layout.settings.lagThreshold) });
    input.setAttribute('aria-label', 'Lag threshold');
    input.title = 'policies.lagThreshold: the lag each topic in the migration may have when kcp fences';
    input.addEventListener('beforeinput', (e) => {
      if (e.data && /\D/.test(e.data)) e.preventDefault();
    });
    input.addEventListener('input', () => {
      // Pasted text keeps only its digits; an empty box changes nothing until a number is typed.
      const digits = input.value.replace(/\D/g, '');
      if (digits !== input.value) input.value = digits;
      if (digits !== '') this.handlers.lagThreshold(Number(digits));
    });
    holder.appendChild(input);
    this.lagThresholdInput = input;
  }

  private buildApps(parent: SVGGElement, wires: SVGGElement): void {
    text(parent, APP.x, APP_TOP - 12, 'box-sub', 'APPLICATIONS');
    APPLICATIONS.forEach((app, a) => {
      const top = appTop(a);
      el('rect', { x: APP.x, y: top, width: APP.w, height: appH(a), rx: 6, class: 'box app-box' }, parent);
      text(parent, APP.x + 12, top + 20, 'app-title', app.name);
      const lines: ['produces' | 'consumes', number][] = [
        ...app.produces.map((t): ['produces', number] => ['produces', t]),
        ...app.consumes.map((t): ['consumes', number] => ['consumes', t]),
      ];
      lines.forEach(([role, topic]) => {
        const y = appLineY(a, topic);
        const g = el('g', { class: 'app-line' }, parent);
        el('rect', { x: APP.x + 12, y: y - 5, width: 10, height: 10, rx: 2, fill: TOPIC_COLOURS[topic] }, g);
        text(g, APP.x + 28, y + 4, 'app-topic', `${this.layout.topics[topic].name} ${role === 'produces' ? 'producer' : 'consumer'}`);
        // Producer i produces to topic i.
        const rate = role === 'produces' ? this.rateSlider(g, y, topic) : undefined;
        this.appLines.push({ g, topic, rate });
        // The line's own wire, in its topic's colour, to the topic's row in the Gateway.
        el('path', { class: 'wire lane in-use', d: curve(appPort(a, topic), rowPort(a, topic)), style: topicStyle(topic) }, wires);
      });
    });
  }

  private rateSlider(parent: SVGGElement, y: number, producer: number): AppLineEls['rate'] {
    const holder = el('foreignObject', { x: RATE.x, y: y - RATE.h / 2, width: RATE.w, height: RATE.h, class: 'slider app-rate' }, parent);
    const input = document.createElement('input');
    Object.assign(input, { type: 'range', min: '1', max: '10', step: '1' });
    input.style.setProperty('--topic', TOPIC_COLOURS[producer]);
    input.setAttribute('aria-label', `${this.layout.topics[producer].name} producer rate`);
    input.addEventListener('input', () => this.handlers.rate(producer, Number(input.value)));
    holder.appendChild(input);
    const value = text(parent, APP.x + APP.w - 10, y + 4, 'value', '', 'end');
    return { input, value };
  }

  private buildGateway(parent: SVGGElement, wires: SVGGElement): void {
    el('rect', { x: GATEWAY.x, y: GATEWAY.y, width: GATEWAY.w, height: GATEWAY.h, rx: 10, class: 'gateway' }, parent);
    text(parent, GATEWAY.x + 16, GATEWAY.y + 24, 'box-title', 'Confluent Gateway');
    text(parent, GATEWAY.x + 16, GATEWAY.y + 40, 'box-sub', 'route migration-route (dynamic): routes each topic');
    text(parent, GATEWAY.x + 16, GATEWAY.y + 56, 'box-sub', 'bootstrap endpoint gateway.example.com:9092');

    this.layout.topics.forEach((t, i) => {
      const y = routeY(i);
      const g = el('g', { class: 'route-row' }, parent);
      el('rect', { x: GW_ROW.x, y: y - GW_ROW.h / 2, width: GW_ROW.w, height: GW_ROW.h, rx: 6, class: 'box route-box' }, g);
      swatch(g, GW_ROW.x, y, GW_ROW.h, i);
      text(g, GW_ROW.x + 14, y - 4, 'label', t.name);
      const route = text(g, GW_ROW.x + 14, y + 12, 'route-text');
      // The topic's own line to its topic on each cluster: no shared trunk.
      const from = { x: GW_ROW.x + GW_ROW.w, y };
      const toSource = el('path', { class: 'wire lane', d: curve(from, { x: ROW.x, y: sourceRowY(i) }), style: topicStyle(i) }, wires);
      const toDest = el('path', { class: 'wire lane', d: curve(from, { x: ROW.x, y: destRowY(i) }), style: topicStyle(i) }, wires);
      this.routeRows.push({ g, route, toSource, toDest });
    });
  }

  private buildClusters(parent: SVGGElement): void {
    el('rect', { x: CLUSTER.x, y: SOURCE.y, width: CLUSTER.w, height: SOURCE.h, rx: 10, class: 'box cluster source' }, parent);
    text(parent, CLUSTER.x + 16, SOURCE.y + 24, 'box-title', 'Source cluster');
    text(parent, CLUSTER.x + 16, SOURCE.y + 40, 'box-sub', 'MSK, Apache Kafka or Confluent Platform');
    el('rect', { x: CLUSTER.x, y: DEST.y, width: CLUSTER.w, height: DEST.h, rx: 10, class: 'box cluster destination' }, parent);
    text(parent, CLUSTER.x + 16, DEST.y + 24, 'box-title', 'Destination cluster');
    text(parent, CLUSTER.x + 16, DEST.y + 40, 'box-sub', 'Confluent Cloud (mirror topics)');

    const cells = (g: SVGGElement, y: number): SVGRectElement[] =>
      Array.from({ length: CELLS }, (_, c) =>
        el('rect', { x: CELL.x + c * CELL.w, y: y + 2, width: CELL.w - 0.8, height: CELL.h, class: 'cell' }, g),
      );
    const endOffset = (g: SVGGElement, y: number): SVGTextElement => text(g, ROW.x + ROW.w - 8, y + 11, 'value', '', 'end');

    this.layout.topics.forEach((t, i) => {
      const sy = sourceRowY(i);
      const dy = destRowY(i);
      const sg = el('g', { class: 'row' }, parent);
      el('rect', { x: ROW.x, y: sy - ROW.h / 2, width: ROW.w, height: ROW.h, rx: 4, class: 'box row-box' }, sg);
      swatch(sg, ROW.x, sy, ROW.h, i);
      text(sg, ROW.x + 12, sy - 5, 'label', t.name);
      const state = text(sg, ROW.x + ROW.w - 8, sy - 5, 'row-state', '', 'end');
      const sourceCells = cells(sg, sy);
      const sourceOffset = endOffset(sg, sy);
      this.sourceRows.push({ g: sg, state, offset: sourceOffset, cells: sourceCells });

      const dg = el('g', {}, parent);
      el('rect', { x: ROW.x, y: dy - ROW.h / 2, width: ROW.w, height: ROW.h, rx: 4, class: 'box' }, dg);
      swatch(dg, ROW.x, dy, ROW.h, i);
      text(dg, ROW.x + 12, dy - 5, 'label', `${t.name} (mirror)`);
      const pill = text(dg, ROW.x + ROW.w - 8, dy - 5, 'pill', '', 'end');
      const lag = text(dg, ROW.x + ROW.w - 80, dy - 5, 'lag-text', '', 'end');
      const destCells = cells(dg, dy);
      const destOffset = endOffset(dg, dy);
      this.destRows.push({ pill, lag, offset: destOffset, cells: destCells });
    });
    this.buildLink(parent);
  }

  // The cluster link: its own box beside both clusters, one lane per topic
  // from the source topic down to its mirror.
  private buildLink(parent: SVGGElement): void {
    el('rect', { x: LINK.x, y: LINK.y, width: LINK.w, height: LINK.h, rx: 10, class: 'box link-box' }, parent);
    text(parent, LINK.x + LINK.w / 2, LINK.y + 24, 'box-title', 'Cluster link', 'middle');
    text(parent, LINK.x + LINK.w / 2, LINK.y + 40, 'box-sub', 'source to destination', 'middle');
    this.layout.topics.forEach((_, i) => el('path', { class: 'wire link', d: this.linkPath(i) }, parent));
    // Readout at the foot of the box, below the lanes.
    const y = destRowY(0) + ROW.h / 2 + 8;
    el('rect', { x: LINK.x + 8, y, width: LINK.w - 16, height: 80, rx: 6, class: 'link-readout' }, parent);
    this.linkLagText = text(parent, LINK.x + LINK.w / 2, y + 16, 'lag-text', '', 'middle');
    this.linkThresholdText = text(parent, LINK.x + LINK.w / 2, y + 32, 'value', '', 'middle');
    this.linkLimitText = text(parent, LINK.x + LINK.w / 2, y + 50, 'value', '', 'middle');
    // The link's throughput limit, set on the link itself.
    const holder = el('foreignObject', { x: LINK.x + 20, y: y + 56, width: LINK.w - 40, height: 16, class: 'slider link-limit' }, parent);
    const input = document.createElement('input');
    Object.assign(input, { type: 'range', min: '5', max: '80', step: '1' });
    input.style.setProperty('--topic', 'var(--link)');
    input.setAttribute('aria-label', 'Link throughput limit');
    input.title = 'The most the cluster link copies, across every topic it mirrors';
    input.addEventListener('input', () => this.handlers.linkLimit(Number(input.value)));
    holder.appendChild(input);
    this.linkLimitInput = input;
  }

  private linkPath(i: number): string {
    const x0 = ROW.x + ROW.w;
    const ys = sourceRowY(i);
    const yd = destRowY(i);
    // The top topic takes the outermost lane.
    const lane = LINK.x + 30 + (TOPICS - 1 - i) * LINK.lane;
    const r = 8;
    return `M ${x0} ${ys} L ${lane - r} ${ys} Q ${lane} ${ys}, ${lane} ${ys + r} L ${lane} ${yd - r} Q ${lane} ${yd}, ${lane - r} ${yd} L ${x0} ${yd}`;
  }

  // ---- geometry for moving dots ----------------------------------------

  /** The path a dot takes from one box to another. */
  private pathBetween(from: NodeId, to: NodeId): string {
    const a = parse(from);
    const b = parse(to);
    if (a.kind === 'source' && b.kind === 'destination') return this.linkPath(a.index);
    // Between an application's line for a topic and that topic's row in the route table, along its wire.
    if (a.kind === 'app') return curve(appPort(a.index, b.index), rowPort(a.index, b.index));
    if (b.kind === 'app') return curve(rowPort(b.index, a.index), appPort(b.index, a.index));
    // Between a topic's route line and its topic on a cluster.
    const row = (n: { kind: string; index: number }): Point =>
      n.kind === 'gateway'
        ? { x: GW_ROW.x + GW_ROW.w, y: routeY(n.index) }
        : { x: ROW.x, y: n.kind === 'source' ? sourceRowY(n.index) : destRowY(n.index) };
    return curve(row(a), row(b));
  }

  private pointsBetween(from: NodeId, to: NodeId): Point[] {
    const key = `${from}>${to}`;
    let points = this.motionPoints.get(key);
    if (points) return points;
    const path = el('path', { d: this.pathBetween(from, to), class: 'motion' }, this.motionLayer);
    const length = path.getTotalLength();
    const n = Math.max(2, Math.ceil(length / 3));
    points = Array.from({ length: n }, (_, i) => {
      const p = path.getPointAtLength((i / (n - 1)) * length);
      return { x: p.x, y: p.y };
    });
    path.remove();
    this.motionPoints.set(key, points);
    return points;
  }

  private launch(hop: Hop): void {
    if (this.dots.length >= MAX_DOTS) return;
    // One size for every dot. A dot carries its topic's colour; a rejection stays red.
    const attrs: Attrs = { r: 2.5, class: `dot ${hop.kind}` };
    if (hop.kind !== 'reject') attrs.style = `fill: ${TOPIC_COLOURS[hop.topic]}`;
    this.dots.push({ hop, el: el('circle', attrs, this.dotLayer), points: this.pointsBetween(hop.from, hop.to) });
  }

  /** Put every dot where it is at model time `time`, and drop those that have arrived. */
  private placeDots(time: number): void {
    this.dots = this.dots.filter((d) => {
      const progress = (time - d.hop.at) / d.hop.duration;
      if (progress >= 1) {
        d.el.remove();
        return false;
      }
      const f = Math.max(0, progress) * (d.points.length - 1);
      const i = Math.floor(f);
      const a = d.points[i];
      const b = d.points[Math.min(i + 1, d.points.length - 1)];
      d.el.setAttribute('cx', String(a.x + (b.x - a.x) * (f - i)));
      d.el.setAttribute('cy', String(a.y + (b.y - a.y) * (f - i)));
      return true;
    });
  }

  // ---- per-frame update -------------------------------------------------

  /**
   * Draw `sim`, start a dot for each new hop, and place every dot where it is
   * at model time `time` (between model steps, so dots move smoothly).
   */
  update(sim: Simulation, hops: Hop[], time: number): void {
    for (const h of hops) this.launch(h);
    this.placeDots(time);
    this.updateSelection(sim);
    this.updateKcp(sim);
    this.updateGateway(sim);
    this.updateApps(sim);
    this.updateClusters(sim);
  }

  private updateSelection(sim: Simulation): void {
    const running = !sim.betweenRuns();
    if (running) this.selectionTitle.textContent = `MIGRATION ${sim.migrationNumber}`;
    else if (sim.allMoved()) this.selectionTitle.textContent = 'EVERY TOPIC IS ON THE DESTINATION';
    else this.selectionTitle.textContent = 'TOPICS TO MIGRATE (tick to select)';
    // A topic can be ticked between migrations until it is on the destination;
    // during a migration the boxes show the topics in it.
    this.topicBoxes.forEach(({ label, box }, i) => {
      const checked = running ? sim.migrating.includes(i) : sim.selection.includes(i);
      const disabled = !sim.canPick(i);
      if (box.checked !== checked) box.checked = checked;
      if (box.disabled !== disabled) box.disabled = disabled;
      const moved = sim.gateway.routed.includes(i) ? ' moved' : '';
      const cls = `topic-choice${disabled ? ' disabled' : ''}${moved}`;
      if (label.className !== cls) label.className = cls;
    });
    const canStart = sim.betweenRuns() && sim.selection.length > 0;
    if (this.startButton.disabled === canStart) this.startButton.disabled = !canStart;
  }

  private updateKcp(sim: Simulation): void {
    // Show the threshold in use, for instance after a rewind, but not while it is being typed in.
    const box = this.lagThresholdInput;
    if (document.activeElement !== box && box.value !== String(sim.settings.lagThreshold)) box.value = String(sim.settings.lagThreshold);
    const a = kcpActivity(sim);
    this.kcpAction.textContent = a.action;
    this.kcpAction.setAttribute('class', `kcp-action${a.wire ? ` ${a.tone}` : ''}`);
    this.kcpDetail.textContent = a.detail;
    this.kcpWires.gateway.setAttribute('class', `kcp-wire${a.wire === 'gateway' ? ` active ${a.tone}` : ''}`);
    this.kcpWires.link.setAttribute('class', `kcp-wire${a.wire === 'link' ? ` active ${a.tone}` : ''}`);
  }

  private updateGateway(sim: Simulation): void {

    sim.topics.forEach((_, i) => {
      const v = this.routeRows[i];
      const route = routeOf(sim.gateway, i);
      v.g.setAttribute('class', `route-row route-${route}`);
      v.route.textContent = routeLabel(route);
      // The line the topic takes is drawn; a fenced topic takes neither.
      v.toSource.setAttribute('class', `wire lane ${route === 'source' ? 'in-use' : 'hidden'}`);
      v.toDest.setAttribute('class', `wire lane ${route === 'destination' ? 'in-use' : 'hidden'}`);
    });
  }

  private updateApps(sim: Simulation): void {
    for (const line of this.appLines) {
      line.g.setAttribute('class', `app-line${topicClass(sim, line.topic)}`);
      if (!line.rate) continue;
      // Show the model's rate, for instance after a rewind.
      const v = sim.settings.producerRates[line.topic];
      if (Number(line.rate.input.value) !== v) line.rate.input.value = String(v);
      line.rate.value.textContent = `${v} msg/s`;
    }
  }

  private updateClusters(sim: Simulation): void {
    let totalLag = 0;
    sim.topics.forEach((t, i) => {
      const lag = sim.lag(t);
      totalLag += lag;
      const src = this.sourceRows[i];
      const dst = this.destRows[i];
      src.g.setAttribute('class', `row${topicClass(sim, i)}`);
      src.state.textContent = topicState(sim, i);
      src.offset.textContent = `offset ${t.sourceEnd}`;
      dst.offset.textContent = `offset ${t.destinationEnd}`;
      // A migrating topic always shows its lag against the threshold, down to 0.
      const migrating = !sim.betweenRuns() && sim.migrating.includes(i);
      const shown = t.mirrorState !== 'STOPPED' && (lag > 0 || migrating);
      dst.lag.textContent = shown ? `lag ${lag}` : '';
      dst.lag.setAttribute('class', migrating ? `lag-text ${lag > sim.settings.lagThreshold ? 'over' : 'met'}` : 'lag-text');
      dst.pill.textContent = t.mirrorState;
      dst.pill.setAttribute('class', `pill ${t.mirrorState}`);

      // Both strips show the same window of offsets, so a message on the source
      // sits directly above its copy on the destination.
      const end = Math.max(t.sourceEnd, t.destinationEnd);
      const first = end - CELLS;
      const stopped = t.mirrorState === 'STOPPED';
      for (let c = 0; c < CELLS; c++) {
        const offset = first + c;
        src.cells[c].setAttribute('class', offset >= 0 && offset < t.sourceEnd ? 'cell source' : 'cell');
        let d = 'cell';
        if (offset >= 0 && offset < t.destinationEnd) d = 'cell destination';
        else if (!stopped && offset >= 0 && offset < t.sourceEnd) d = 'cell lag';
        dst.cells[c].setAttribute('class', d);
      }
    });
    this.linkLagText.textContent = `total lag ${totalLag}`;
    this.linkLimitText.textContent = `throughput limit ${sim.settings.linkLimit} msg/s`;
    if (Number(this.linkLimitInput.value) !== sim.settings.linkLimit) this.linkLimitInput.value = String(sim.settings.linkLimit);
    this.linkThresholdText.textContent = `lag threshold ${sim.settings.lagThreshold} per topic`;
    this.linkLagText.setAttribute('class', totalLag > 0 ? 'lag-text' : 'lag-text zero');
  }

  clearDots(): void {
    this.dotLayer.replaceChildren();
    this.dots = [];
  }

  get element(): SVGSVGElement {
    return this.root;
  }
}

/** How a topic shows wherever it appears: in the running migration, fenced, or moved. Follows what the Gateway applies. */
function topicClass(sim: Simulation, topic: number): string {
  let cls = '';
  if (!sim.betweenRuns() && sim.migrating.includes(topic)) cls += ' migrating';
  if (sim.gateway.fenced.includes(topic)) cls += ' fenced';
  if (sim.gateway.routed.includes(topic)) cls += ' moved';
  return cls;
}

function routeLabel(r: TopicRoute): string {
  return r === 'fenced' ? 'fenced' : `to ${r}`;
}

function topicState(sim: Simulation, topic: number): string {
  if (sim.gateway.routed.includes(topic)) return 'on destination';
  if (sim.gateway.fenced.includes(topic)) return `fenced, migration ${sim.migrationNumber}`;
  if (!sim.betweenRuns() && sim.migrating.includes(topic)) return `migration ${sim.migrationNumber}`;
  return '';
}

interface KcpActivity {
  action: string;
  detail: string;
  // The wire kcp is making a change through, if any.
  wire: 'gateway' | 'link' | null;
  tone: 'fence' | 'switch' | 'promote' | '';
}

/** What kcp is doing, in words, and which of its wires is busy. */
function kcpActivity(sim: Simulation): KcpActivity {
  const names = (ts: number[]) => ts.map((t) => sim.topics[t].name).join(', ');
  const quiet = (action: string, detail = ''): KcpActivity => ({ action, detail, wire: null, tone: '' });
  // The fence patch lights its wire for up to a step's hold, until zero lag is reached.
  const patchingFence = sim.now - sim.fencedAt < timing.stageHold;
  const fence: KcpActivity = { action: 'patching the Gateway: fence', detail: `${names(sim.migrating)} fenced`, wire: 'gateway', tone: 'fence' };
  switch (sim.phase) {
    case 'idle':
    case 'completed':
      return quiet('', sim.allMoved() ? '' : 'Tick the topics to migrate, then press Start migration.');
    case 'started':
      return quiet('migration started', names(sim.migrating));
    case 'lag-check':
      return quiet('checking lag', `Waiting until every topic in this migration has lag at or below ${sim.settings.lagThreshold}.`);
    case 'threshold-met':
      return quiet('checking the Gateway', 'Making sure the Gateway takes on route rule changes.');
    case 'fencing':
      return patchingFence ? fence : quiet('waiting for zero lag', `${names(sim.migrating)}, fenced`);
    case 'zero-lag':
      return quiet('zero lag', `${names(sim.migrating)}, ready to promote`);
    case 'promoting':
      return { action: 'promoting on the cluster link', detail: names(sim.migrating), wire: 'link', tone: 'promote' };
    case 'promoted':
      return quiet('promoted', `${names(sim.migrating)}: mirror topics STOPPED`);
    case 'switching':
      return { action: 'patching the Gateway: switch', detail: `${names(sim.migrating)} to the destination, fence removed`, wire: 'gateway', tone: 'switch' };
  }
}
