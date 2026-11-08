//! Memlane worker v1: stdout JSONL only, local model files, scoped raw retrieval.
mod backend;
mod code;
use backend::{Backend, Record};
use serde::Deserialize;
use serde_json::{json, Value};
use std::{collections::HashSet, io::{self, Write}, path::PathBuf, sync::Arc, time::Duration};
use tokio::{io::{AsyncBufReadExt, AsyncReadExt, BufReader}, sync::{mpsc, oneshot}};
use uuid::Uuid;

const PROTOCOL: u32 = 1;
const WORKER_VERSION: &str = "0.0.2";
const MAX_LINE: usize = 1024*1024;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    protocol: u32,
    id: String,
    op: String,
    #[serde(default)] record: Option<Record>,
    #[serde(default)] dataset: Option<Uuid>,
    #[serde(default)] query: String,
    #[serde(default)] top_k: usize,
    #[serde(default)] chunk_ids: Vec<Uuid>,
    #[serde(default)] target: String,
    #[serde(default)] code_import: Option<Uuid>,
    #[serde(default)] code_query: Option<code::Query>,
    #[serde(default = "timeout_default")] timeout_ms: u64,
}
fn timeout_default() -> u64 { 120000 }

fn emit(value: Value) -> backend::Result<()> {
    let mut stdout = io::stdout().lock();
    serde_json::to_writer(&mut stdout, &value)?;
    stdout.write_all(b"\n")?; stdout.flush()?; Ok(())
}

fn error(id: &str, code: &str, message: &str) -> Value {
    json!({"protocol":PROTOCOL,"id":id,"ok":false,"error":{"code":code,"message":message}})
}
fn success(id: &str, result: Value) -> Value {
    json!({"protocol":PROTOCOL,"id":id,"ok":true,"result":result})
}

async fn perform(engine: &Arc<Backend>, input: Request) -> backend::Result<Value> {
    match input.op.as_str() {
        "index" => engine.index(&input.record.ok_or("record required")?).await,
        "search" => Ok(json!({"candidates":engine.search(input.dataset.ok_or("dataset required")?,&input.query,input.top_k).await?})),
        "remove" => {engine.remove(input.dataset.ok_or("dataset required")?,&input.chunk_ids).await?;Ok(json!({"removed":input.chunk_ids.len()}))},
        "code_build" => code::build(engine.code_root(),input.dataset.ok_or("dataset required")?,input.code_import.ok_or("code import required")?).await,
        "code_query" => code::query(engine.code_root(),input.code_query.ok_or("code query required")?).await,
        _ => Err("unsupported operation".into()),
    }
}

#[tokio::main]
async fn main() -> backend::Result<()> {
    let args: Vec<String> = std::env::args().collect();
    if args.len()!=3 {return Err("expected absolute data and model directories".into());}
    let data=PathBuf::from(&args[1]);let model=PathBuf::from(&args[2]);
    if !data.is_absolute() || !model.is_absolute() {return Err("directories must be absolute".into());}
    emit(json!({"protocol":PROTOCOL,"event":"hello","worker_version":WORKER_VERSION,"ready":false,
        "engine":"cognee-rs","engine_commit":"151905c302fc9b5b9aad88d6b13931c09f0eac30",
        "adapter_patch_sha256":"c73d00e0f09c0ef481fb0c1422be98206dcabafea14c10e0ed6d9ddf98292e89"}))?;
    std::fs::create_dir_all(&data)?;
    let ownership=std::fs::OpenOptions::new().read(true).write(true).create(true).truncate(false).open(data.join(".worker.lock"))?;
    if let Err(e)=ownership.try_lock(){emit(error("","owner_conflict",&e.to_string()))?;return Err(e.into());}
    let engine = match Backend::new(data,&model) {
        Ok(e)=>Arc::new(e),
        Err(e)=>{emit(error("","startup_failed",&e.to_string()))?;return Err(e);},
    };
    emit(json!({"protocol":PROTOCOL,"event":"ready","worker_version":WORKER_VERSION,"ready":true,
        "generation":engine.generation(),"model":engine.recipe.id,"model_revision":engine.recipe.revision,
        "model_load_ms":engine.load_ms,"capabilities":["raw_index","raw_search","remove","cancel","timeout","code_build","code_query"],
        "graph_ready":true,"code_recipe":"enola-0.4.30-v351"}))?;
    let mut stdin=BufReader::new(tokio::io::stdin());
    let (completed_tx,mut completed_rx)=mpsc::channel::<(String,Value)>(1);
    let mut active: Option<(String,Option<oneshot::Sender<()>>)>=None;
    let mut used_ids=HashSet::<String>::new();
    let mut buffer=Vec::new();
    loop {
        let mut limited=(&mut stdin).take((MAX_LINE+1-buffer.len()) as u64);
        tokio::select! {
            Some((_id,response))=completed_rx.recv() => {
                active=None;
                emit(response)?;
            },
            read=limited.read_until(b'\n',&mut buffer) => {
                let count=read?;
                if count==0 {break;}
                if buffer.len()>MAX_LINE {emit(error("","message_too_large","message exceeds 1 MiB"))?;break;}
                let input:Request=match serde_json::from_slice(&buffer) {
                    Ok(r)=>r,Err(_)=>{buffer.clear();emit(error("","bad_request","invalid JSON or request fields"))?;continue;},
                };
                buffer.clear();
                if input.protocol!=PROTOCOL {emit(error(&input.id,"protocol_mismatch","requires protocol 1"))?;continue;}
                if input.id.is_empty() || input.id.len()>128 {emit(error(&input.id,"bad_request","invalid request id"))?;continue;}
                if used_ids.len()>=10000 || !used_ids.insert(input.id.clone()) {emit(error(&input.id,"duplicate_id","request IDs must be unique; reconnect after 10000 requests"))?;continue;}
                match input.op.as_str() {
                    "handshake" | "health" => emit(success(&input.id,json!({"worker_version":WORKER_VERSION,"ready":true,"busy":active.is_some(),"generation":engine.generation(),"code_recipe":"enola-0.4.30-v351"})))?,
                    "cancel" => {
                        let mut matched=false;
                        if active.as_ref().is_some_and(|(id,_)|*id==input.target) {
                            if let Some((_,sender))=active.as_mut(){if let Some(sender)=sender.take(){let _=sender.send(());matched=true;}}
                        }
                        emit(success(&input.id,json!({"cancel_requested":matched})))?;
                    },
                    "shutdown" => {if let Some((_,Some(sender)))=active.take(){let _=sender.send(());}emit(success(&input.id,json!({"stopping":true})))?;break;},
                    "index" | "search" | "remove" | "code_build" | "code_query" => {
                        if active.is_some(){emit(error(&input.id,"busy","one engine operation at a time"))?;continue;}
                        if input.timeout_ms==0 || input.timeout_ms>120000 {emit(error(&input.id,"bad_request","invalid timeout"))?;continue;}
                        let id=input.id.clone();let deadline=input.timeout_ms;
                        let engine=Arc::clone(&engine);let completed=completed_tx.clone();
                        let (sender,mut cancel)=oneshot::channel();active=Some((id.clone(),Some(sender)));
                        tokio::spawn(async move {
                            let response=tokio::select! {
                                biased;
                                _=&mut cancel=>error(&id,"cancelled","operation cancelled; partial derived chunks are not a completed index"),
                                _=tokio::time::sleep(Duration::from_millis(deadline))=>error(&id,"timeout","operation exceeded timeout; no completed index receipt"),
                                result=perform(&engine,input)=>match result {
                                    Ok(r)=>success(&id,r),Err(e)=>error(&id,"engine_error",&e.to_string()),
                                },
                            };
                            let _=completed.send((id,response)).await;
                        });
                    },
                    _=>emit(error(&input.id,"unsupported_operation","operation not supported by this worker"))?,
                }
            },
        }
    }
    if let Some((_,Some(sender)))=active.take(){let _=sender.send(());}
    Ok(())
}
