"""Bounded extraction + independent batch judgment before official construction.

Not installed in submission until a durable per-observation audit owner is wired.
A model verdict is evidence of that judgment, not a general entailment proof.
"""
import hashlib
import inspect
import json
from typing import Literal
from pydantic import BaseModel, ConfigDict, Field

RECIPE = 'memlane-grounded-graph-v1'
MAX_CHUNKS = 1
MAX_BYTES = 256 * 1024
MAX_EDGES = 64
EXTRACT_PROMPT = '''Extract only source-grounded entities and directed relations from the frozen text.
For every candidate bind the actual grammatical subject and object to exact UTF-8 byte spans;
include minimal support spans and predicate wording. A document title, system name, or nearby
entity is NOT the unstated actor of a passive, generic, or omitted-subject statement. Do not
turn a generic request into a named message type without an explicit definition. Keep generic
subjects when the source supports them. Preserve negation, conditions, quantities and direction.
Legitimate evidence may span several sentences. For aliases or pronouns give the explicit
antecedent mention and necessary bridge spans. Ambiguous attribution must remain a candidate
with its actual mentions, never invent a concrete actor. Use stable unique opaque ASCII node and candidate IDs (letters/digits/underscore); names remain exact human text.
Do not use outside knowledge. Return only the requested schema.'''
JUDGE_PROMPT = '''Independently judge each supplied directed relation against ONLY the frozen raw text.
The extractor and its proposed coreference/evidence are untrusted hypotheses. Assess whether
THIS source entity performs THIS predicate toward THIS target, including direction, conditions,
negation, quantities and alias/coreference attribution. Every qualifier in the candidate description is part of the claim and must also be entailed. Co-occurrence, a document title/system
name, or exact quote substring is not entailment. Generic/passive subject cannot be reassigned
to a nearby named entity without evidence. Cross-sentence relations ARE allowed when the text
explicitly links the mentions or unambiguously resolves their reference. Mark entailed only
when supported, unsupported when contradicted or attributed to a different subject, unknown
when evidence is insufficient/ambiguous. Provide a concise reason and exact support spans for
entailed verdicts. Return exactly one verdict for every candidate ID, no omissions or extras.
Do not repair, replace or add edges. Return only the requested schema.'''

class Strict(BaseModel):
    model_config = ConfigDict(extra='forbid')

class Span(Strict):
    start: int = Field(ge=0)
    end: int = Field(ge=1)
    text: str = Field(min_length=1, max_length=8192)

class Mention(Strict):
    entity: str
    span: Span
    form: Literal['literal', 'alias', 'coreference']
    antecedent: Span | None
    bridges: list[Span] = Field(max_length=8)

class Node(Strict):
    id: str = Field(min_length=1,max_length=256,pattern=r'^[A-Za-z0-9_.:-]+$')
    name: str = Field(min_length=1,max_length=1024)
    type: str = Field(min_length=1,max_length=256)
    description: str = Field(max_length=4096)

class Candidate(Strict):
    id: str = Field(min_length=1,max_length=128,pattern=r'^[A-Za-z0-9_.:-]+$')
    source: str
    target: str
    predicate: str = Field(min_length=1,max_length=256,pattern=r'^[a-z][a-z0-9_]*$')
    description: str = Field(min_length=1,max_length=4096)
    subject: Mention
    object: Mention
    predicate_span: Span
    support: list[Span] = Field(min_length=1,max_length=16)

class Extraction(Strict):
    nodes: list[Node] = Field(max_length=128)
    candidates: list[Candidate] = Field(max_length=MAX_EDGES)

class Verdict(Strict):
    id: str
    state: Literal['entailed','unsupported','unknown']
    reason: str = Field(min_length=1,max_length=2048)
    support: list[Span] = Field(max_length=16)

class Judgment(Strict):
    verdicts: list[Verdict] = Field(max_length=MAX_EDGES)


def span_valid(raw, span):
    data = raw.encode('utf-8')
    if not 0 <= span.start < span.end <= len(data):
        raise ValueError('grounding_span_bounds')
    try:
        actual = data[span.start:span.end].decode('utf-8')
    except UnicodeDecodeError:
        raise ValueError('grounding_span_utf8') from None
    if actual != span.text:
        raise ValueError('grounding_span_mismatch')


def integrity(raw, extraction):
    nodes = {n.id:n for n in extraction.nodes}
    if len(nodes) != len(extraction.nodes):
        raise ValueError('grounding_duplicate_node')
    seen = set()
    relations=set()
    for edge in extraction.candidates:
        if edge.id in seen or edge.source not in nodes or edge.target not in nodes:
            raise ValueError('grounding_candidate_identity')
        seen.add(edge.id)
        relation=(edge.source,edge.target,edge.predicate)
        if relation in relations:
            raise ValueError('grounding_duplicate_relation')
        relations.add(relation)
        spans = [edge.predicate_span, *edge.support]
        for mention, endpoint in ((edge.subject,edge.source),(edge.object,edge.target)):
            if mention.entity != endpoint:
                raise ValueError('grounding_endpoint_binding')
            spans.extend([mention.span, *mention.bridges])
            if mention.form == 'literal':
                if mention.antecedent is not None or mention.bridges or mention.span.text.casefold() != nodes[endpoint].name.casefold():
                    raise ValueError('grounding_literal_binding')
            else:
                if mention.antecedent is None or not mention.bridges or mention.antecedent.text.casefold() != nodes[endpoint].name.casefold():
                    raise ValueError('grounding_coreference_missing')
                spans.append(mention.antecedent)
        for span in spans:
            span_valid(raw,span)
        # The minimal supporting set must actually contain all proposed bindings,
        # including antecedents/bridges. This is integrity, never entailment.
        for span in spans:
            if not any(s.start <= span.start and span.end <= s.end for s in edge.support):
                raise ValueError('grounding_support_coverage')


def checked_verdicts(raw, extraction, judgment):
    ids = [e.id for e in extraction.candidates]
    verdicts = {v.id:v for v in judgment.verdicts}
    if len(verdicts)!=len(judgment.verdicts) or set(verdicts)!=set(ids):
        raise ValueError('grounding_verdict_ids')
    for verdict in verdicts.values():
        if verdict.state == 'entailed' and not verdict.support:
            raise ValueError('grounding_judge_support_missing')
        for span in verdict.support:
            span_valid(raw,span)
    return verdicts


def canonical(value):
    return json.dumps(value,ensure_ascii=False,sort_keys=True,separators=(',',':'))


class GroundedExtractor:
    """Actual calculate_chunk_graphs callback; output is consumed by Cognee.

    complete uses the existing schema adapter; persist_audit must durably store
    every candidate/verdict (including rejected/unknown) and return a digest-bound
    receipt BEFORE this callback releases any entailed edge to construction.
    No default file sink, no implicit paid retry or fallback exists.
    """
    def __init__(self, complete, persist_audit=None):
        self.complete, self.persist_audit = complete, persist_audit
        self.chunks_seen = 0

    async def __call__(self, data_chunks, graph_model, custom_prompt=None, **kwargs):
        if self.persist_audit is None:
            raise ValueError('grounding_audit_owner_required')
        if custom_prompt is not None or not 1 <= len(data_chunks) <= MAX_CHUNKS:
            raise ValueError('grounding_batch_contract')
        self.chunks_seen += len(data_chunks)
        if self.chunks_seen > MAX_BYTES:
            raise ValueError('grounding_material_chunk_budget')
        graphs=[]
        for chunk in data_chunks:
            raw=chunk.text
            if not isinstance(raw,str) or not raw or len(raw.encode())>MAX_BYTES:
                raise ValueError('grounding_input_limit')
            extraction = Extraction.model_validate(await self.complete(
                text_input=raw,system_prompt=EXTRACT_PROMPT,response_model=Extraction))
            integrity(raw,extraction)
            # A distinct request/context, not extraction's self-reported support.
            judgment = Judgment.model_validate(await self.complete(
                text_input=canonical({'raw':raw,'candidates':extraction.model_dump()}),
                system_prompt=JUDGE_PROMPT,response_model=Judgment))
            verdicts=checked_verdicts(raw,extraction,judgment)
            audit={'recipe':RECIPE,'chunk':str(chunk.id),'raw_sha256':hashlib.sha256(raw.encode()).hexdigest(),
                   'extraction':extraction.model_dump(),'judgment':judgment.model_dump(),
                   'calls':{'extraction':1,'independent_batch_judgment':1,'automatic_retries':0},
                   'claim':'model_judgment_not_universal_entailment_proof'}
            digest=hashlib.sha256(canonical(audit).encode()).hexdigest()
            receipt=self.persist_audit(audit,digest,chunk=chunk)
            if inspect.isawaitable(receipt):receipt=await receipt
            if not isinstance(receipt,dict) or receipt.get('persisted') is not True or receipt.get('digest')!=digest or not receipt.get('observation'):
                raise ValueError('grounding_audit_not_persisted')
            edges=[{'source_node_id':e.source,'target_node_id':e.target,'relationship_name':e.predicate,'description':e.description}
                   for e in extraction.candidates if verdicts[e.id].state=='entailed']
            graphs.append(graph_model.model_validate({'nodes':[n.model_dump() for n in extraction.nodes],'edges':edges}))
        return graphs


def official_edge_bindings(chunk, graph):
    """Use the installed SDK's actual constructor on an isolated chunk copy.

    This handles normalization, duplicate node names and timestamp direction in
    the same code as official construction. Never infer graph IDs from names.
    """
    from cognee.modules.graph.utils import construct_data_points_and_edges
    from cognee.modules.graph.utils.expand_with_nodes_and_edges import _with_timestamps_as_leaves
    if _with_timestamps_as_leaves(graph).edges != graph.edges:
        raise ValueError('grounding_official_direction_changed')
    result=[]
    for edge in graph.edges:
        isolated=chunk.model_copy(deep=True)
        single=graph.model_copy(update={'edges':[edge]})
        _,edges=construct_data_points_and_edges([isolated],[single])
        if len(edges)!=1:
            raise ValueError('grounding_official_edge_mapping_required')
        identity=next(iter(edges))
        result.append({'source_node_id':identity.source_id,'target_node_id':identity.target_id,
                       'relationship_name':identity.relationship_name})
    return result


class MaterialGroundedExtractor(GroundedExtractor):
    """Submission-local owner: durable pending before official construction,
    publish only after exact completed SDK edge/document/chunk/run evidence.
    """
    def __init__(self, material, document, system_root):
        from cognee.infrastructure.llm.LLMGateway import LLMGateway
        from cognee_grounding_audit import record_pending
        self.material,self.document,self.system_root=material,str(document),system_root
        self.pending=[]
        async def persist(audit,digest,*,chunk):
            from cognee.shared.data_models import KnowledgeGraph
            judgments={v['id']:v for v in audit['judgment']['verdicts']}
            if str(getattr(chunk,'document_id',None) or getattr(getattr(chunk,'is_part_of',None),'id',''))!=self.document:
                raise ValueError('grounding_chunk_document_changed')
            if chunk.text not in material['text']:
                raise ValueError('grounding_chunk_source_changed')
            for candidate in audit['extraction']['candidates']:
                candidate=dict(candidate,nodes=audit['extraction']['nodes'])
                observation={k:material[k] for k in ('library','operation','instance','dataset','project','memory','revision','digest')}
                observation.update(document=self.document,chunk=str(chunk.id),chunk_text_digest=audit['raw_sha256'],recipe=RECIPE,
                                   candidate=candidate,judge=judgments[candidate['id']],budget=audit['calls'])
                # Capture the exact SDK constructor identity even for rejected
                # candidates, but only entailed ones can ever be published.
                graph=KnowledgeGraph.model_validate({'nodes':audit['extraction']['nodes'],'edges':[dict(source_node_id=candidate['source'],target_node_id=candidate['target'],relationship_name=candidate['predicate'],description=candidate['description'])]})
                binding=official_edge_bindings(chunk,graph)[0]
                audit_id=record_pending(system_root,observation)
                self.pending.append(dict(audit_id=audit_id,chunk=str(chunk.id),binding=binding,state=judgments[candidate['id']]['state']))
            return dict(persisted=True,digest=digest,observation='pending-durable')
        super().__init__(LLMGateway.acreate_structured_output,persist)

    async def publish_completed(self, run_ids):
        from uuid import UUID
        from sqlalchemy import select, and_
        from cognee.infrastructure.databases.relational import get_relational_engine
        from cognee.modules.provenance.edge_evidence.models import ProvenanceEdgeEvidence as E
        from cognee.modules.pipelines.models import PipelineRun, PipelineRunStatus
        from cognee_grounding_audit import publish_completed
        runs=[UUID(str(r)) for r in run_ids]
        if not runs:
            raise ValueError('grounding_completed_run_required')
        for pending in self.pending:
            if pending['state']!='entailed':
                continue
            b=pending['binding']
            statement=select(E.edge_id,E.pipeline_run_id,E.dataset_id,E.data_id,E.chunk_id,E.source_node_id,E.destination_node_id,E.relationship_name).join(PipelineRun,and_(PipelineRun.pipeline_run_id==E.pipeline_run_id,PipelineRun.status==PipelineRunStatus.DATASET_PROCESSING_COMPLETED)).where(
                E.dataset_id==UUID(self.material['dataset']),E.data_id==UUID(self.document),E.chunk_id==UUID(pending['chunk']),
                E.source_node_id==UUID(b['source_node_id']),E.destination_node_id==UUID(b['target_node_id']),E.relationship_name==b['relationship_name'],E.pipeline_run_id.in_(runs))
            async with get_relational_engine().get_async_session() as session:
                rows=(await session.execute(statement)).all()
            identities=set()
            expected=(self.material['dataset'],self.document,pending['chunk'],b['source_node_id'],b['target_node_id'],b['relationship_name'])
            for row in rows:
                if str(row[1]) not in {str(r) for r in runs} or tuple(str(v) for v in row[2:]) != expected:
                    raise ValueError('grounding_completed_edge_mapping_required')
                identities.add((str(row[0]),str(row[1])))
            if len(identities)!=1:
                raise ValueError('grounding_completed_edge_mapping_required')
            edge_id,run_id=next(iter(identities))
            completion=dict(instance=self.material['instance'],dataset=self.material['dataset'],document=self.document,
                            chunk=pending['chunk'],edge_id=edge_id,run_id=run_id,pipeline_status='PipelineRunCompleted',**b)
            publish_completed(self.system_root,pending['audit_id'],completion)
            pending['published']=True

    def mark_failed(self,code):
        from cognee_grounding_audit import mark_failed
        for item in self.pending:
            if item.get('published'):
                continue
            mark_failed(self.system_root,item['audit_id'],code)


def relation_grounding(system_root, *, instance, dataset, edge_id, sources, materials):
    """No audit is inferred from whole-chunk provenance or an old observation.
    Pending/missing store is explicitly unreviewed; IO failure stays unknown.
    """
    from cognee_grounding_audit import lookup_published
    published=[]
    try:
        for source in sources:
            audit=lookup_published(system_root,instance=instance,dataset=dataset,edge_id=edge_id,
                                   document=source['document'],chunk=source['chunk'])
            if audit is not None:
                o=audit['observation']
                material=materials.get(source['document'])
                if o['recipe']!=RECIPE or material is None or any(o[k]!=material.get(k) for k in ('library','instance','dataset','project','memory','revision','digest')) or o['chunk_text_digest']!=hashlib.sha256(source['quote'].encode()).hexdigest():
                    continue
                c=audit['completion']
                if c['edge_id']!=edge_id or c['document']!=source['document'] or c['chunk']!=source['chunk']:
                    raise ValueError('grounding_lookup_mapping_changed')
                published.append({'audit_id':audit['audit_id'],'recipe':o['recipe'],'run_id':c['run_id'],'edge_id':c['edge_id'],'document':c['document'],'chunk':c['chunk']})
    except Exception:
        return {'state':'audit_unavailable','claim':'provenance_is_not_semantic_verification','audits':[]}
    return {'state':'model_entailed' if published else 'unreviewed',
            'claim':'independent_model_judgment_not_universal_entailment_proof', 'audits':published}


async def graph_materials(dataset, documents):
    """Only graph-referenced documents in the already authorized dataset.
    Do not sample the first page of a larger dataset or cap its total size.
    """
    from uuid import UUID
    from sqlalchemy import select
    from cognee.infrastructure.databases.relational import get_relational_engine
    from cognee.modules.data.models import Data
    ids=sorted({UUID(str(document)) for document in documents},key=str)
    materials={}
    for start in range(0,len(ids),500):
        query=select(Data.id,Data.external_metadata).where(Data.dataset_id==dataset,Data.id.in_(ids[start:start+500]))
        async with get_relational_engine().get_async_session() as session:
            rows=(await session.execute(query)).all()
        for identity,metadata in rows:
            materials[str(identity)]=(metadata or {}).get('memlane_material_v1')
    return materials
