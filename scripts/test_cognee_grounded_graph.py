"""No model/DB: real official callback consumer and in-memory construction."""
import asyncio
import hashlib
import importlib
import json
import os
from pathlib import Path
import sys
import unittest
import tempfile
import atexit
from unittest.mock import patch
from uuid import uuid4

# All SDK imports happen AFTER private roots are installed; even importing
# Cognee initializes logging and may otherwise rotate a real user log.
_private = tempfile.TemporaryDirectory(prefix='memlane-grounded-contract-')
atexit.register(_private.cleanup)
_base=Path(_private.name)
for _name in ('system','data','cache','logs','home'):(_base/_name).mkdir()
os.environ.update(SYSTEM_ROOT_DIRECTORY=str(_base/'system'),DATA_ROOT_DIRECTORY=str(_base/'data'),CACHE_ROOT_DIRECTORY=str(_base/'cache'),LOGS_ROOT_DIRECTORY=str(_base/'logs'),COGNEE_LOGS_DIR=str(_base/'logs'),HOME=str(_base/'home'),PYTHON_DOTENV_DISABLED='1',HF_HUB_OFFLINE='1',TELEMETRY_DISABLED='true',COGNEE_TRACING_ENABLED='false',STRUCTURED_OUTPUT_FRAMEWORK='litellm_native',LITELLM_LOCAL_MODEL_COST_MAP='true')
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'engine/python'))
from cognee_grounded_graph import GroundedExtractor, Extraction, Judgment, canonical, official_edge_bindings


def span(raw,text):
    start=raw.encode().index(text.encode());return dict(start=start,end=start+len(text.encode()),text=text)

def fixture(raw='Orchid accepts envelopes. Every envelope keeps trace_key.', subject='Orchid', target='trace_key', predicate='keeps'):
    support=[span(raw,raw)]
    mention=lambda name,entity:dict(entity=entity,span=span(raw,name),form='literal',antecedent=None,bridges=[])
    candidate=dict(id='candidate-1',source='s',target='t',predicate=predicate,description=f'{subject} {predicate} {target}',
                   subject=mention(subject,'s'),object=mention(target,'t'),predicate_span=span(raw,predicate),support=support)
    return raw,dict(nodes=[dict(id='s',name=subject,type='Concept',description=''),dict(id='t',name=target,type='Concept',description='')],candidates=[candidate])


class Grounded(unittest.IsolatedAsyncioTestCase):
    async def consume(self,raw,extraction,state='unsupported', *, judge_ids=None, sink=True, storage_failure=False):
        from cognee.shared.data_models import KnowledgeGraph
        from cognee.modules.chunking.models.DocumentChunk import DocumentChunk
        consumer=importlib.import_module('cognee.tasks.graph.extract_graph_from_data')
        chunk=DocumentChunk.model_construct(id=uuid4(),text=raw,chunk_size=len(raw),chunk_index=0,cut_type='test',is_part_of=None)
        calls=[];audits=[];constructed=[]
        async def complete(**q):
            calls.append(q)
            if q['response_model'] is Extraction:return extraction
            ids=judge_ids if judge_ids is not None else [e['id'] for e in extraction['candidates']]
            return dict(verdicts=[dict(id=i,state=state,reason='Independent fixed fixture verdict',support=[span(raw,raw)] if state=='entailed' else []) for i in ids])
        def persist(audit,digest,**kwargs):
            if storage_failure:raise ValueError('audit_capacity_reached')
            audits.append(audit)
            return dict(persisted=True,digest=digest,observation='synthetic-audit-receipt')
        callback=GroundedExtractor(complete,persist if sink else None)
        if os.environ.get('GROUNDING_RED_LEGACY')=='1':
            # Frozen old seam: a schema-valid graph is returned without an
            # independent judgment. The consumer assertion below must be red.
            async def callback(*args,**kwargs):
                return [KnowledgeGraph.model_validate(dict(nodes=extraction['nodes'],edges=[dict(source_node_id='s',target_node_id='t',relationship_name=extraction['candidates'][0]['predicate'])]))]
        async def integrate(chunks,graphs,*args,**kwargs):
            from cognee.modules.graph.utils import construct_data_points_and_edges
            _,edges=construct_data_points_and_edges(chunks,graphs)
            constructed.extend(edges)
            # Exercise the real SDK's non-LLM summary path after construction.
            from cognee.tasks.summarization.build_summary_from_extraction import build_summary_from_extraction
            from cognee.infrastructure.llm.LLMGateway import LLMGateway
            with patch.object(LLMGateway,'acreate_structured_output',side_effect=AssertionError('summary must not call a model')):
                summary=await build_summary_from_extraction(chunks[0],KnowledgeGraph)
            self.assertEqual(summary is not None,bool(edges))
            if graphs[0].edges:
                expected=official_edge_bindings(chunks[0],graphs[0]);self.assertEqual(expected,[dict(source_node_id=e.source_id,target_node_id=e.target_id,relationship_name=e.relationship_name) for e in edges])
            return chunks
        with patch.object(consumer,'get_configured_ontology_resolver',return_value=None),patch.object(consumer,'get_configured_ontology_mode',return_value='test'),patch.object(consumer,'integrate_chunk_graphs',side_effect=integrate):
            await consumer.extract_graph_from_data([chunk],KnowledgeGraph,calculate_chunk_graphs=callback)
        return calls,audits,constructed

    async def test_real_consumer_independent_judge_rejects_wrong_subject(self):
        for name in ('Orchid','Quartz'):
            raw,extraction=fixture(raw=f'{name} accepts envelopes. Every envelope keeps trace_key.',subject=name)
            calls,audits,edges=await self.consume(raw,extraction)
            self.assertEqual(edges,[])
            self.assertEqual(len(calls),2)
            self.assertIs(calls[1]['response_model'],Judgment)
            self.assertIn('untrusted',calls[1]['system_prompt'])
            self.assertEqual(audits[0]['judgment']['verdicts'][0]['state'],'unsupported')

    async def test_unknown_is_audited_not_constructed(self):
        raw,extraction=fixture();calls,audits,edges=await self.consume(raw,extraction,'unknown')
        self.assertFalse(edges);self.assertEqual(len(audits),1);self.assertEqual(len(calls),2)

    async def test_cross_sentence_coreference_and_generic_subject_survive(self):
        raw='Delta handles records. It sends them to Archive.'
        _,extraction=fixture(raw=raw,subject='Delta',target='Archive',predicate='sends')
        extraction['candidates'][0]['subject']=dict(entity='s',span=span(raw,'It'),form='coreference',antecedent=span(raw,'Delta'),bridges=[span(raw,'Delta handles records.')])
        calls,audits,edges=await self.consume(raw,extraction,'entailed');self.assertEqual(len(edges),1)
        raw,extraction=fixture(subject='envelope')
        _,_,edges=await self.consume(raw,extraction,'entailed');self.assertEqual(len(edges),1)

    async def test_missing_duplicate_extra_judge_ids_fail_closed(self):
        raw,extraction=fixture()
        for ids in ([],['candidate-1','candidate-1'],['candidate-1','extra']):
            with self.assertRaisesRegex(ValueError,'grounding_verdict_ids'):await self.consume(raw,extraction,judge_ids=ids)

    async def test_missing_durable_owner_does_not_call_model_or_construct(self):
        raw,extraction=fixture()
        with self.assertRaisesRegex(ValueError,'grounding_audit_owner_required'):await self.consume(raw,extraction,sink=False)

    async def test_duplicate_relation_candidates_not_silently_overwritten(self):
        raw,extraction=fixture();extraction['candidates'].append(dict(extraction['candidates'][0],id='candidate-2'))
        with self.assertRaisesRegex(ValueError,'grounding_duplicate_relation'):
            await self.consume(raw,extraction,'entailed')

    async def test_audit_storage_failure_prevents_construction(self):
        raw,extraction=fixture()
        with self.assertRaisesRegex(ValueError,'audit_capacity_reached'):
            await self.consume(raw,extraction,'entailed',storage_failure=True)

    async def test_forged_mentions_and_utf8_ranges_rejected(self):
        raw,extraction=fixture();extraction['candidates'][0]['subject']['span']=span(raw,'envelope')
        with self.assertRaisesRegex(ValueError,'grounding_literal_binding'):await self.consume(raw,extraction,'entailed')
        raw,extraction=fixture(raw='兰花 keeps 标签。',subject='兰花',target='标签')
        extraction['candidates'][0]['subject']['span']['start']=1
        with self.assertRaisesRegex(ValueError,'grounding_span_utf8'):await self.consume(raw,extraction,'entailed')


class OwnerAudit(unittest.IsolatedAsyncioTestCase):
    async def build(self):
        from types import SimpleNamespace
        from cognee.shared.data_models import KnowledgeGraph
        from cognee.modules.chunking.models.DocumentChunk import DocumentChunk
        from cognee.infrastructure.llm.LLMGateway import LLMGateway
        from cognee_grounded_graph import MaterialGroundedExtractor
        raw,extraction=fixture(raw='Orchid keeps trace_key.')
        material={k:str(uuid4()) for k in ('library','operation','instance','dataset','project','memory')}
        material.update(revision=1,digest=hashlib.sha256(raw.encode()).hexdigest(),text=raw)
        document=str(uuid4());chunk=DocumentChunk.model_construct(id=uuid4(),text=raw,chunk_size=len(raw),chunk_index=0,cut_type='test',is_part_of=None,document_id=document)
        async def complete(**q):
            return extraction if q['response_model'] is Extraction else dict(verdicts=[dict(id='candidate-1',state='entailed',reason='Explicit subject',support=[span(raw,raw)])])
        owner=Path(tempfile.mkdtemp(prefix='owner-',dir=_private.name));owner=owner.resolve()
        with patch.object(LLMGateway,'acreate_structured_output',side_effect=complete):
            callback=MaterialGroundedExtractor(material,document,str(owner))
            graphs=await callback([chunk],KnowledgeGraph)
        source=dict(document=document,chunk=str(chunk.id),quote=raw)
        return callback,material,source,graphs

    async def test_pending_failure_and_exact_published_lookup(self):
        from types import SimpleNamespace
        from cognee_grounded_graph import relation_grounding
        callback,material,source,graphs=await self.build()
        pending=callback.pending[0];run,edge=str(uuid4()),str(uuid4());b=pending['binding']
        def lookup(edge_id=edge, materials=None):
            return relation_grounding(callback.system_root,instance=material['instance'],dataset=material['dataset'],edge_id=edge_id,sources=[source],materials=materials if materials is not None else {source['document']:material})
        self.assertEqual(lookup()['state'],'unreviewed')
        # SDK graph construction can already have succeeded; failure publishing
        # the independent audit never upgrades whole-chunk provenance.
        with patch('cognee.infrastructure.databases.relational.get_relational_engine',side_effect=RuntimeError('synthetic publish failure')):
            with self.assertRaises(RuntimeError):await callback.publish_completed([run])
        self.assertEqual(lookup()['state'],'unreviewed')
        row=(edge,run,material['dataset'],source['document'],source['chunk'],b['source_node_id'],b['target_node_id'],b['relationship_name'])
        class Session:
            async def __aenter__(self):return self
            async def __aexit__(self,*args):pass
            async def execute(self,statement):return SimpleNamespace(all=lambda:[row])
        engine=SimpleNamespace(get_async_session=lambda:Session())
        with patch('cognee.infrastructure.databases.relational.get_relational_engine',return_value=engine):
            await callback.publish_completed([run])
        self.assertEqual(lookup()['state'],'model_entailed')
        self.assertEqual(lookup(str(uuid4()))['state'],'unreviewed')
        wrong=dict(material,library=str(uuid4()))
        self.assertEqual(lookup(materials={source['document']:wrong})['state'],'unreviewed')
        original=source['chunk'];source['chunk']=str(uuid4());self.assertEqual(lookup()['state'],'unreviewed');source['chunk']=original

    async def test_completed_mapping_wrong_run_document_chunk_rejected(self):
        from types import SimpleNamespace
        callback,material,source,_=await self.build();p=callback.pending[0];b=p['binding'];run,edge=str(uuid4()),str(uuid4())
        good=[edge,run,material['dataset'],source['document'],source['chunk'],b['source_node_id'],b['target_node_id'],b['relationship_name']]
        for index in (1,2,3,4,5,6,7):
            row=good.copy();row[index]=str(uuid4())
            class Session:
                async def __aenter__(self):return self
                async def __aexit__(self,*args):pass
                async def execute(self,statement):return SimpleNamespace(all=lambda:[row])
            with patch('cognee.infrastructure.databases.relational.get_relational_engine',return_value=SimpleNamespace(get_async_session=lambda:Session())):
                with self.assertRaisesRegex(ValueError,'grounding_completed_edge_mapping_required'):await callback.publish_completed([run])

    async def test_stream_more_than_sixteen_chunks_with_exact_two_calls_each(self):
        from types import SimpleNamespace
        from cognee.shared.data_models import KnowledgeGraph
        raw,extraction=fixture();calls=[]
        async def complete(**q):
            calls.append(q)
            return extraction if q['response_model'] is Extraction else dict(verdicts=[dict(id='candidate-1',state='unknown',reason='No attribution',support=[])])
        def persist(audit,digest,**kwargs):return dict(persisted=True,digest=digest,observation='private-fixture')
        callback=GroundedExtractor(complete,persist)
        for i in range(17):
            graph=await callback([SimpleNamespace(id=uuid4(),text=raw)],KnowledgeGraph)
            self.assertEqual(graph[0].edges,[])
        self.assertEqual(len(calls),34)


class BrowsingConsumer(unittest.IsolatedAsyncioTestCase):
    build=OwnerAudit.build
    async def test_actual_browse_filters_pending_and_unavailable_without_deleting_source(self):
        from contextlib import asynccontextmanager
        from types import SimpleNamespace
        from uuid import UUID
        import cognee_browsing
        callback,material,source,_=await self.build();b=callback.pending[0]['binding'];edge=str(uuid4())
        root=Path(callback.system_root)
        (root/'memlane-service-identity.json').write_text(json.dumps({'instance':material['instance']}))
        class App:
            def __init__(self):self.routes={}
            def get(self,path):
                def wrap(fn):self.routes[path]=fn;return fn
                return wrap
            post=get
        @asynccontextmanager
        async def context(*args,**kwargs):yield
        async def authorized(*args):return SimpleNamespace(owner_id=uuid4())
        async def records(*args,**kwargs):return [SimpleNamespace(id=UUID(source['document']),external_metadata={'memlane_material_v1':material})]
        async def evidence(*args,**kwargs):return [SimpleNamespace(edge_id=UUID(edge),data_id=UUID(source['document']),chunk_id=UUID(source['chunk']),chunk_index=0)]
        class Graph:
            async def get_graph_data(self):return [
                (b['source_node_id'],{'type':'Entity','name':'Orchid'}),
                (b['target_node_id'],{'type':'Entity','name':'trace_key'}),
                (source['chunk'],{'type':'DocumentChunk','text':source['quote'],'document_id':source['document']})],[(b['source_node_id'],b['target_node_id'],b['relationship_name'],{'edge_object_id':edge}),(source['chunk'],b['source_node_id'],'contains')]
        async def graph():return Graph()
        with patch.dict(os.environ,{'SYSTEM_ROOT_DIRECTORY':str(root)}),patch('cognee.modules.data.methods.get_authorized_dataset',side_effect=authorized),patch('cognee_grounded_graph.graph_materials',return_value={source['document']:material}),patch('cognee.context_global_variables.set_database_global_context_variables',side_effect=context),patch('cognee.infrastructure.databases.graph.get_graph_engine',side_effect=graph),patch('cognee.modules.provenance.edge_evidence.lookup.get_edge_evidence_records',side_effect=evidence),patch('cognee_processing_adapter.install'),patch('cognee_submission.install'):
            app=App();cognee_browsing.install(app)
            endpoint=app.routes['/api/v1/memlane/datasets/{dataset_id}/graph']
            page=await endpoint(UUID(material['dataset']),UUID(material['instance']),SimpleNamespace(id=uuid4()))
            self.assertEqual(page['relations'],[])
            self.assertEqual(page['unsupported_relations'],1)
            self.assertEqual(page['grounding_unreviewed_relations'],1)
            self.assertEqual(len(page['mentions']),1)
            with patch('cognee_grounding_audit.lookup_published',side_effect=ValueError('private-audit-unavailable')):
                page=await endpoint(UUID(material['dataset']),UUID(material['instance']),SimpleNamespace(id=uuid4()))
            self.assertEqual(page['relations'],[])
            self.assertEqual(page['grounding_audit_unavailable_relations'],1)
            self.assertEqual(len(page['mentions']),1)
            from cognee_grounding_audit import publish_completed
            publish_completed(str(root),callback.pending[0]['audit_id'],dict(instance=material['instance'],dataset=material['dataset'],document=source['document'],chunk=source['chunk'],run_id=str(uuid4()),edge_id=edge,pipeline_status='PipelineRunCompleted',**b))
            page=await endpoint(UUID(material['dataset']),UUID(material['instance']),SimpleNamespace(id=uuid4()))
            self.assertEqual(len(page['relations']),1)
            self.assertEqual(page['relations'][0]['grounding']['state'],'model_entailed')
            self.assertEqual(page['unsupported_relations'],0)
            compact=page['relations'][0]['grounding']['audits'][0]
            self.assertEqual(set(compact),{'audit_id','recipe','run_id','edge_id','document','chunk'})
            self.assertNotIn('observation',compact)
            original=Graph.get_graph_data
            async def oversized(self):
                nodes,edges=await original(self)
                nodes.extend((str(uuid4()),{'type':'Entity','name':'bounded','description':'x'*3000}) for _ in range(3000))
                return nodes,edges
            from fastapi import HTTPException
            with patch.object(Graph,'get_graph_data',oversized):
                with self.assertRaises(HTTPException) as failure:
                    await endpoint(UUID(material['dataset']),UUID(material['instance']),SimpleNamespace(id=uuid4()))
            self.assertEqual(failure.exception.status_code,413)



if __name__=='__main__':unittest.main()
