import { useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { flushSync } from 'react-dom';
import { MantineProvider } from '@mantine/core';
import '@mantine/core/styles.css';
import BusinessGraphCanvas from '@/modules/business/canvas/BusinessGraphCanvas';
import type { BusinessCanvasHandle } from '@/modules/business/canvas/BusinessCanvas';
import type { Spotlight } from '@/modules/business/canvas/businessDraw';
import type { BusinessEntity } from '@/modules/business/sceneTypes';
import type { VisualizationPayload } from '@/modules/business/types';
import { computeBrainState, countLinksTouching } from '@/modules/business/computeBrainState';
import NodePanel from '@/modules/business/panels/NodePanel';
import SourcesRail from '@/modules/business/panels/SourcesRail';
import SourceDetailCard from '@/modules/business/panels/SourceDetailCard';
import { computeSourceDetail } from '@/modules/business/computeSourceDetail';
import { computeWhatIfRemoval } from '@/modules/business/computeWhatIfRemoval';
import { useShortestPath } from '@/modules/business/useShortestPath';

interface Frame { epoch: number; project: string; instance: string; dataset: string; payload: VisualizationPayload }
interface MyGoRuntime { call: (name: string, ...args: unknown[]) => Promise<unknown> }
declare global { interface Window {
  mygo?: MyGoRuntime;
  memlaneOfficialGraph: { apply: (frame: Frame) => boolean; clear: (epoch: number) => boolean };
} }

let currentEpoch = -1;
let renderFrame: (frame: Frame | null) => void;
function hostEvent(frame: Frame, node: BusinessEntity) {
  // Only an identity selection leaves the page. Host resolves provenance and
  // rechecks the epoch and current project identity before following any source.
  if (frame.epoch !== currentEpoch) return;
  void window.mygo?.call('OfficialGraph.Select', {epoch: frame.epoch, project: frame.project,
    instance: frame.instance, dataset: frame.dataset, node: node.id}).catch(() => {});
}

function Graph({frame}: {frame: Frame}) {
  const brain = useMemo(() => computeBrainState(frame.payload.nodes, frame.payload.links, frame.payload.color_maps.node_set), [frame]);
  const canvas = useRef<BusinessCanvasHandle>(null);
  const cardRefs = useRef<Record<string, HTMLElement | null>>({});
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [targetId, setTargetId] = useState<string | null>(null);
  const selected = selectedId ? brain.entityById[selectedId] ?? null : null;
  const target = targetId ? brain.entityById[targetId] ?? null : null;
  const [source, setSource] = useState<string | null>(null);
  const [spotlight, setSpotlight] = useState<Spotlight | null>(null);
  const path = useShortestPath(brain.graphLinks, selected?.id ?? null, target?.id ?? null);
  const focusSets = useMemo(() => source ? new Set([source]) : null, [source]);
  const encoding = brain.encodings.source;
  const sourceDetail = source && brain.sourceNames.includes(source) ? computeSourceDetail(source, brain, encoding) : null;
  function select(node: BusinessEntity, shift: boolean) {
    if (shift && selected) setTargetId(node.id);
    else { setSelectedId(node.id); setTargetId(null); setSource(null); hostEvent(frame, node); }
  }
  return <div className="relative h-full w-full overflow-hidden bg-[#0E1526]">
    <BusinessGraphCanvas ref={canvas} brainState={brain} colorMode="source" selectedId={selected?.id ?? null}
      onSelectEntity={select} onBackgroundClick={() => {setSelectedId(null); setTargetId(null); setSpotlight(null);}}
      spotlight={spotlight} focusSets={focusSets} focusTypes={null} sourceCardRefs={cardRefs}
      activeDatasetId={frame.dataset} pathIds={path.pathIds} pathEdgeKeys={path.pathEdgeKeys}/>
    <div className="host-toolbar"><button onClick={() => canvas.current?.fit(true)}>适应窗口</button>
      <button onClick={() => canvas.current?.goToAltimeterLevel(2)}>查看节点</button>
      <span className="text-[11px] text-[#7E8CA6]">已加载子图 · {frame.payload.nodes.length} 个节点</span></div>
    <div className="absolute left-3 top-14 bottom-8 w-52 overflow-y-auto pointer-events-none [&>*]:pointer-events-auto">
      <SourcesRail brainState={brain} encoding={encoding} focusSets={focusSets}
        registerCardRef={(name, el) => {cardRefs.current[name] = el;}}
        onToggleFocus={(name) => {setSource(source === name ? null : name); setSelectedId(null); setTargetId(null);}}/>
    </div>
    <SourceDetailCard detail={selected ? null : sourceDetail} onClose={() => setSource(null)}/>
    <NodePanel entity={selected} docCount={selected ? countLinksTouching(brain.docLinks, selected.id) : 0}
      connectionCount={selected ? countLinksTouching(brain.graphLinks, selected.id) : 0} events={[]}
      onClose={() => {setSelectedId(null); setTargetId(null);}} pathTargetName={target?.name ? String(target.name) : null}
      pathHops={target && path.pathIds.size ? path.pathIds.size - 1 : null} onClearPath={() => setTargetId(null)}
      onSimulateRemoval={() => {if (!selected) return; const result = computeWhatIfRemoval(selected.id, brain.graphLinks);
        const startedAt = performance.now(); setSpotlight({ids: result.orphanedIds, startedAt, until: startedAt + 12000, source:'whatIf'});}}/>
  </div>;
}

function App() {
  const [frame, setFrame] = useState<Frame | null>(null);
  renderFrame = setFrame;
  return <MantineProvider forceColorScheme="dark">{frame ? <Graph key={[frame.project,frame.instance,frame.dataset].join(':')} frame={frame}/>
    : <div className="host-status">等待当前项目的官方图谱</div>}</MantineProvider>;
}
flushSync(() => createRoot(document.getElementById('root')!).render(<App/>));
window.memlaneOfficialGraph = {
  apply(frame) {
    if (!Number.isSafeInteger(frame?.epoch) || frame.epoch <= currentEpoch || !frame.project || !frame.instance || !frame.dataset
      || !Array.isArray(frame.payload?.nodes) || !Array.isArray(frame.payload?.links) || !frame.payload.color_maps
      || frame.payload.nodes.length > 1000 || frame.payload.links.length > 50000) return false;
    currentEpoch = frame.epoch;
    flushSync(() => renderFrame(frame));
    return true;
  },
  clear(epoch) {
    if (!Number.isSafeInteger(epoch) || epoch < currentEpoch) return false;
    currentEpoch = epoch;
    flushSync(() => renderFrame(null));
    return true;
  },
};
void window.mygo?.call('OfficialGraph.Ready').catch(() => {});
