//! Immutable, independently scoped code snapshots in real Cognee Ladybug stores.
//! No Enola execution, repository paths, LLM or raw Cypher are exposed here.
use cognee::graph::{GraphDBTrait, LadybugAdapter, EdgeData};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{collections::{HashMap, HashSet, BTreeSet}, fs, io::{Read, Write}, path::{Path, PathBuf}};
use uuid::Uuid;
use crate::backend::Result;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct SourceFile {path:String,sha256:String,bytes:u64,lines:usize}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Manifest {worktree:String,common_dir:String,commit:String,recipe:String,digest:String,files:Vec<SourceFile>}
#[derive(Clone, Deserialize, Serialize)]
pub struct Node {id:String,extractor_id:String,name:String,kind:String,file:String,line:usize,language:String,symbol_kind:String,file_sha:String,unresolved:bool}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Edge {source:String,target:String,kind:String}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Graph {dataset:Uuid,snapshot:Uuid,manifest:Manifest,extractor:String,nodes:Vec<Node>,edges:Vec<Edge>}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Query {pub dataset:Uuid,pub snapshot:Uuid,mode:String,text:String,node:String,limit:usize,hops:usize}

fn safe_path(p:&str)->bool {
    !p.is_empty() && p.len()<=1024 && !p.contains('\\') && !p.chars().any(char::is_control) && !Path::new(p).is_absolute()
        && p.split('/').all(|s|!s.is_empty() && s!="." && s!="..")
}
fn hash(p:&str)->bool {p.len()==64 && p.bytes().all(|c|c.is_ascii_digit() || (b'a'..=b'f').contains(&c))}
fn validate(g:&Graph)->Result<()> {
    if g.extractor!="enola-0.4.30-v351" || !g.manifest.recipe.starts_with("memlane-code-v1:")
        || !hash(&g.manifest.digest) || g.nodes.len()>20000 || g.edges.len()>60000 || g.manifest.files.len()>2000
        || g.snapshot!=Uuid::new_v5(&g.dataset,g.manifest.digest.as_bytes()) {return Err("invalid code graph recipe/snapshot/budget".into());}
    let mut files=HashMap::new();let mut total=0_u64;
    for f in &g.manifest.files {
        total=total.checked_add(f.bytes).ok_or("code byte overflow")?;
        if !safe_path(&f.path)||!hash(&f.sha256)||f.bytes>1024*1024||f.lines==0||files.insert(f.path.as_str(),f).is_some(){return Err("invalid code source manifest".into());}
    }
    if total>32*1024*1024{return Err("code source exceeds byte budget".into());}
    let mut ids=HashSet::new();
    for n in &g.nodes {
        if Uuid::parse_str(&n.id).is_err()||!ids.insert(n.id.clone())||n.name.len()>1024||n.symbol_kind.len()>128||n.extractor_id.len()>128||n.extractor_id.chars().any(char::is_control)||n.name.chars().any(char::is_control)||n.symbol_kind.chars().any(char::is_control){return Err("invalid code node".into());}
        if n.kind=="external" {
            if !n.unresolved||!n.file.is_empty()||!n.file_sha.is_empty()||n.line!=0{return Err("invalid external code node".into());}
        } else if n.kind=="module" {
            if n.unresolved||!safe_path(&n.file)||!files.keys().any(|p|p.starts_with(&(n.file.clone()+"/"))){return Err("invalid module code source".into());}
        } else {
            let f=files.get(n.file.as_str()).ok_or("code node missing source")?;
            if !["file","symbol","dependency"].contains(&n.kind.as_str())||n.unresolved||n.file_sha!=f.sha256||n.line>f.lines{return Err("code node source mismatch".into());}
        }
        if !["", "python", "typescript"].contains(&n.language.as_str()){return Err("unsupported code language".into());}
    }
    let mut edges=HashSet::new();
    for e in &g.edges {
        if !ids.contains(&e.source)||!ids.contains(&e.target)||!["contains","calls","imports","declares"].contains(&e.kind.as_str())
            ||!edges.insert((e.source.clone(),e.target.clone(),e.kind.clone())){return Err("invalid code graph relation".into());}
    }
    Ok(())
}
fn bounded(path:&Path)->Result<Vec<u8>> {
    if fs::symlink_metadata(path)?.file_type().is_symlink(){return Err("code import must not be a symlink".into());}
    let mut f=fs::File::open(path)?;let mut data=Vec::new();(&mut f).take(32*1024*1024+1).read_to_end(&mut data)?;
    if data.len()>32*1024*1024{return Err("code import exceeds budget".into());}Ok(data)
}
fn folder(root:&Path,dataset:Uuid,snapshot:Uuid)->PathBuf {root.join("code").join(dataset.to_string()).join(snapshot.to_string())}
fn receipt(g:&Graph,raw:&[u8])->Value {
    json!({"dataset":g.dataset,"snapshot":g.snapshot,"source_digest":g.manifest.digest,
        "graph_digest":Uuid::new_v5(&Uuid::NAMESPACE_OID,raw),"nodes":g.nodes.len(),"edges":g.edges.len(),"extractor":g.extractor})
}
fn sync_file(path:&Path,raw:&[u8])->Result<()> {
    let mut f=fs::OpenOptions::new().create_new(true).write(true).open(path)?;f.write_all(raw)?;f.sync_all()?;Ok(())
}
async fn load(db:&LadybugAdapter,g:&Graph)->Result<(HashMap<String,Node>,Vec<Edge>)> {
    let (all,connections)=db.get_graph_data().await?;
    if all.len()!=g.nodes.len()||connections.len()!=g.edges.len(){return Err("stored code graph incomplete".into());}
    let expected:HashMap<&str,&Node>=g.nodes.iter().map(|n|(n.id.as_str(),n)).collect();
    let mut nodes=HashMap::<String,Node>::new();
    for (id,props) in all {
        let mut value=serde_json::to_value(props)?;value["id"]=json!(&id);let n:Node=serde_json::from_value(value)?;
        let original=expected.get(id.as_str()).ok_or("stored code node unknown")?;
        if serde_json::to_value(&n)?!=serde_json::to_value(original)?||nodes.insert(id,n).is_some(){return Err("stored code node differs from frozen source".into());}
    }
    let expected_edges:HashSet<(&str,&str,&str)>=g.edges.iter().map(|e|(e.source.as_str(),e.target.as_str(),e.kind.as_str())).collect();
    let mut seen=HashSet::new();let mut edges=Vec::new();
    for (source,target,kind,_) in connections {
        if !expected_edges.contains(&(source.as_str(),target.as_str(),kind.as_str()))||!seen.insert((source.clone(),target.clone(),kind.clone())){return Err("stored code edge differs from frozen input".into());}
        edges.push(Edge{source,target,kind});
    }
    Ok((nodes,edges))
}

pub async fn build(root:&Path,dataset:Uuid,import:Uuid)->Result<Value> {
    let raw=bounded(&root.join("code-imports").join(format!("{import}.json")))?;
    let g:Graph=serde_json::from_slice(&raw)?;validate(&g)?;
    if dataset!=g.dataset{return Err("code dataset mismatch".into());}
    let dest=folder(root,g.dataset,g.snapshot);
    if dest.exists() {
        if bounded(&dest.join("import.json"))?!=raw{return Err("code snapshot replay conflicts".into());}
        // A replay still proves the actual database is usable, not just a file.
        let db=LadybugAdapter::new(dest.join("graph.lbug").to_str().ok_or("invalid graph path")?).await?;
        let checked=load(&db,&g).await;db.close().await?;checked?;
        return Ok(receipt(&g,&raw));
    }
    let parent=dest.parent().ok_or("code parent required")?;fs::create_dir_all(parent)?;
    let stage=parent.join(format!(".building-{}",Uuid::new_v4()));fs::create_dir(&stage)?;
    let db=LadybugAdapter::new(stage.join("graph.lbug").to_str().ok_or("invalid graph path")?).await?;
    let work:Result<()>=async {
        db.initialize().await?;
        for batch in g.nodes.chunks(256) {
            let nodes:Vec<Value>=batch.iter().map(|n| {let mut v=serde_json::to_value(n).expect("code node serializable");v["type"]=json!(format!("code_{}",n.kind));v}).collect();
            db.add_nodes_raw(nodes).await?;
        }
        for batch in g.edges.chunks(256) {
            let edges:Vec<EdgeData>=batch.iter().map(|e|(e.source.clone(),e.target.clone(),e.kind.clone(),HashMap::new())).collect();
            db.add_edges(&edges).await?;
        }
        load(&db,&g).await?;
        if !db.flush().await?{return Err("code graph checkpoint failed".into());}
        Ok(())
    }.await;
    let closed=db.close().await;
    if let Err(e)=work {let _=fs::remove_dir_all(&stage);return Err(e);}
    if let Err(e)=closed {let _=fs::remove_dir_all(&stage);return Err(e.into());}
    sync_file(&stage.join("import.json"),&raw)?;
    fs::File::open(&stage)?.sync_all()?;
    fs::rename(&stage,&dest)?;fs::File::open(parent)?.sync_all()?;
    Ok(receipt(&g,&raw))
}

pub async fn query(root:&Path,q:Query)->Result<Value> {
    if q.limit==0||q.limit>50||q.hops==0||q.hops>3||q.text.len()>512||q.node.len()>128
        ||!["symbols","files","calls","dependencies","impact"].contains(&q.mode.as_str()){return Err("invalid code query".into());}
    let dest=folder(root,q.dataset,q.snapshot);let raw=bounded(&dest.join("import.json"))?;
    let g:Graph=serde_json::from_slice(&raw)?;validate(&g)?;
    if g.dataset!=q.dataset||g.snapshot!=q.snapshot{return Err("code snapshot mismatch".into());}
    let db=LadybugAdapter::new(dest.join("graph.lbug").to_str().ok_or("invalid graph path")?).await?;
    let data=load(&db,&g).await;db.close().await?;
    let (mut nodes,edges)=data?;
    let mut seeds:BTreeSet<String>=BTreeSet::new();let needle=q.text.to_lowercase();
    if !q.node.is_empty(){if !nodes.contains_key(&q.node){return Err("code node not in snapshot".into());};seeds.insert(q.node.clone());}
    else {for (id,n) in &nodes {
        let eligible=match q.mode.as_str(){"symbols"=>n.kind=="symbol","files"=>n.kind=="file","dependencies"=>n.kind=="dependency",_=>true};
        if eligible&&(n.name.to_lowercase().contains(&needle)||n.file.to_lowercase().contains(&needle)){seeds.insert(id.clone());}
    }}
    let mut truncated=seeds.len()>q.limit;let mut selected:BTreeSet<String>=seeds.into_iter().take(q.limit).collect();let mut links=Vec::<Edge>::new();
    if ["calls","dependencies","impact"].contains(&q.mode.as_str()) {
        let rounds=if q.mode=="impact"{q.hops}else{2};let mut frontier=selected.clone();
        for _ in 0..rounds {
            let mut next=BTreeSet::new();
            for e in &edges {
                let reverse=q.mode=="impact";
                let matched=if reverse{frontier.contains(&e.target)&&e.kind!="declares"}else{frontier.contains(&e.source)&&["contains",if q.mode=="calls"{"calls"}else{"imports"}].contains(&e.kind.as_str())};
                if !matched{continue;}
                let target=if reverse{&e.source}else{&e.target};
                if !selected.contains(target) {
                    if selected.len()>=100 {truncated=true;continue;}
                    selected.insert(target.clone());next.insert(target.clone());
                }
                if links.len()<300 {links.push(e.clone());}else{truncated=true;}
            }
            frontier=next;if frontier.is_empty(){break;}
        }
    }
    links.sort_by(|a,b|(&a.source,&a.target,&a.kind).cmp(&(&b.source,&b.target,&b.kind)));links.dedup_by(|a,b|a.source==b.source&&a.target==b.target&&a.kind==b.kind);
    let result:Vec<Node>=selected.into_iter().filter_map(|id|nodes.remove(&id)).collect();
    Ok(json!({"receipt":receipt(&g,&raw),"manifest":g.manifest,"nodes":result,"edges":links,"truncated":truncated}))
}
