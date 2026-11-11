import { createTimer, engine } from 'animejs';
import { timing, type Phase } from './sim/model';
import { Timeline } from './sim/timeline';
import { Scene, type SceneHandlers } from './view/scene';

// 1x on the speed slider is this engine speed: one model second (timing.second) takes a second on screen.
const BASE_SPEED = timing.second / 1000;
// The slider's stops: fine below 1x, coarser above. Pause stops the model.
const SPEEDS = [0.1, 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4, 5];

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

const svgRoot = document.getElementById('scene') as unknown as SVGSVGElement;
const logList = $<HTMLOListElement>('log');
const logPanel = logList.closest('.log-panel') as HTMLElement;
const pauseBtn = $<HTMLButtonElement>('pause');
const scrub = $<HTMLInputElement>('scrub');
const markersEl = $<HTMLDivElement>('markers');
// The scrubber moves in model steps, so the end of the run is always a value it can show.
scrub.step = String(timing.step);

// ---- the run -------------------------------------------------------------

// Every setting is a control in the drawing; a run starts from the model's defaults.
let timeline = new Timeline({});
const handlers: SceneHandlers = {
  pick: (topic) => change({ type: 'toggle-topic', topic }),
  start: () => change({ type: 'start' }),
  rate: (producer, value) => change({ type: 'producer-rate', producer, value }),
  linkLimit: (value) => change({ type: 'setting', key: 'linkLimit', value }),
  lagThreshold: (value) => change({ type: 'setting', key: 'lagThreshold', value }),
};
let scene = new Scene(svgRoot, timeline.sim, handlers);
let clockOffset = 0;
// The model time on screen: the clock's, which runs smoothly between model steps.
let shownTime = 0;
let logShown = 0;
let scrubbing = false;

// One clock for the whole page: the model advances on an Anime.js timer, so
// engine.speed slows or speeds up the model and every animation together.
const clock = createTimer({
  duration: Infinity,
  onUpdate: (t) => {
    shownTime = t.currentTime - clockOffset;
    const hops = timeline.advanceTo(shownTime);
    render(hops);
  },
});

function render(hops: Parameters<Scene['update']>[1]): void {
  scene.update(timeline.sim, hops, shownTime);
  renderLog();
  renderTimeline();
}

// The latest line goes at the bottom. The log follows it while the viewer is at
// the bottom, and stays put if they have scrolled up to read earlier lines.
function renderLog(): void {
  const log = timeline.sim.log;
  if (logShown >= log.length) return;
  const atBottom = logPanel.scrollHeight - logPanel.scrollTop - logPanel.clientHeight < 8;
  for (; logShown < log.length; logShown++) {
    const line = log[logShown];
    const li = document.createElement('li');
    li.className = line.tone;
    li.textContent = line.text;
    logList.append(li);
  }
  if (atBottom) logPanel.scrollTop = logPanel.scrollHeight;
}

const MARKER_LABEL: Partial<Record<Phase, string>> = {
  'lag-check': 'Waiting for threshold',
  'threshold-met': 'Threshold met',
  fencing: 'Fence',
  'zero-lag': 'Zero lag',
  promoting: 'Promoting',
  promoted: 'Promoted',
  switching: 'Switch',
  completed: 'Done',
};
// Each marker's colour says what kind of step it starts.
const MARKER_KIND: Partial<Record<Phase, string>> = {
  started: 'start',
  'lag-check': 'wait',
  'threshold-met': 'threshold',
  fencing: 'fence',
  'zero-lag': 'threshold',
  promoting: 'promote',
  promoted: 'promote',
  switching: 'switch',
  completed: 'done',
};
const MARKER_ROW = 18;
let markersDrawn = '';

function renderTimeline(): void {
  const end = Math.max(timeline.end, 1);
  if (!scrubbing) {
    scrub.max = String(timeline.end);
    scrub.value = String(timeline.now);
  }
  // Markers move as the end of the run grows; rebuild only when that changes them.
  const key = `${timeline.end}|${timeline.markers.length}`;
  if (key === markersDrawn) return;
  markersDrawn = key;
  markersEl.replaceChildren();
  const width = markersEl.clientWidth || 1;
  const rowEnds = [-Infinity, -Infinity]; // right edge, in px, of the last marker in each row
  for (const m of timeline.markers) {
    // Each migration's start is marked with its number.
    const label = m.phase === 'started' ? `Migration ${m.migration}` : MARKER_LABEL[m.phase];
    if (!label) continue;
    // A marker is a label in the lane and a line from it down to its moment on the scrubber.
    const mark = document.createElement('div');
    mark.className = `marker ${MARKER_KIND[m.phase] ?? ''}`;
    const line = document.createElement('span');
    line.className = 'marker-line';
    const b = document.createElement('button');
    b.textContent = label;
    b.addEventListener('click', () => rewindTo(m.at));
    mark.append(line, b);
    markersEl.append(mark);
    // Markers close in time would overlap: put each in the first row with room.
    const x = (m.at / end) * width;
    const half = b.offsetWidth / 2 + 2;
    const row = rowEnds[0] <= x - half ? 0 : rowEnds[1] <= x - half ? 1 : 0;
    rowEnds[row] = x + half;
    mark.style.left = `${x}px`;
    // Keep a label near either end inside the lane; its line stays on its moment.
    const shift = Math.max(half + 2 - x, 0) - Math.max(x + half + 2 - width, 0);
    b.style.transform = `translateX(calc(-50% + ${shift}px))`;
    b.style.top = `${4 + row * MARKER_ROW}px`;
    line.style.top = `${4 + row * MARKER_ROW + 16}px`;
  }
}

let paused = false;

function setPaused(value: boolean): void {
  if (value === paused) return;
  paused = value;
  if (paused) clock.pause();
  else clock.resume();
  pauseBtn.setAttribute('aria-pressed', String(paused));
  pauseBtn.textContent = paused ? 'Resume' : 'Pause';
  pauseBtn.classList.toggle('primary', paused);
}

function rewindTo(time: number): void {
  setPaused(true);
  seek(time);
}

function seek(time: number): void {
  const inFlight = timeline.seek(time);
  clockOffset = clock.currentTime - timeline.now;
  shownTime = timeline.now;
  scene.clearDots();
  logList.replaceChildren();
  logShown = 0;
  // Re-create the dots that were mid-way at that moment.
  render(inFlight.map((f) => f.hop));
}

function reset(): void {
  timeline = new Timeline({});
  scene.clearDots();
  scene = new Scene(svgRoot, timeline.sim, handlers);
  clockOffset = clock.currentTime;
  shownTime = 0;
  logList.replaceChildren();
  logShown = 0;
  markersDrawn = '';
  render([]);
}

// ---- wiring ---------------------------------------------------------------

$('reset').addEventListener('click', reset);

// Dragging the timeline or clicking a marker pauses, so the moment can be
// looked at; Resume carries on from there.
scrub.addEventListener('pointerdown', () => {
  scrubbing = true;
  setPaused(true);
});
// The release can land anywhere on the page, not only on the slider.
window.addEventListener('pointerup', () => (scrubbing = false));
scrub.addEventListener('change', () => (scrubbing = false));
scrub.addEventListener('input', () => rewindTo(Number(scrub.value)));

pauseBtn.addEventListener('click', () => setPaused(!paused));

const speed = $<HTMLInputElement>('speed');
const applySpeed = () => {
  const v = SPEEDS[Number(speed.value)];
  engine.speed = v * BASE_SPEED;
  $<HTMLOutputElement>('speed-out').textContent = `${v}x`;
};
speed.addEventListener('input', applySpeed);
applySpeed();

// Every model setting goes through the timeline as a timed input, so a rewind
// replays it and a change after a rewind starts a new branch.
// Render straight away so a change made while paused shows.
function change(input: Parameters<Timeline['input']>[0]): void {
  timeline.input(input);
  render([]);
}
render([]);
