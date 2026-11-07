mod backend;
use serde::Deserialize;
use serde_json::{json, Value};
use std::{io::Read, path::PathBuf};
use uuid::Uuid;

#[derive(Deserialize)]
#[serde(tag="op", rename_all="snake_case", deny_unknown_fields)]
enum Input {
    Index { records: Vec<backend::Record> },
    Search { dataset: Uuid, query: String, top_k: usize },
    Remove { dataset: Uuid, chunk_ids: Vec<Uuid> },
}

#[tokio::main]
async fn main() -> backend::Result<()> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 { return Err("expected data and model directories".into()); }
    let mut raw = String::new(); std::io::stdin().read_to_string(&mut raw)?;
    let input: Input = serde_json::from_str(&raw)?;
    let engine = std::sync::Arc::new(backend::Backend::new(PathBuf::from(&args[1]), &PathBuf::from(&args[2]))?);
    let result: Value = match input {
        Input::Index { records } => {
            let mut receipts = Vec::new();
            for record in records {receipts.push(engine.index(&record).await?);}
            json!({"receipts":receipts})
        },
        Input::Search {dataset,query,top_k} => json!({"candidates":engine.search(dataset,&query,top_k).await?}),
        Input::Remove {dataset,chunk_ids} => {engine.remove(dataset,&chunk_ids).await?;json!({"removed":chunk_ids.len()})},
    };
    println!("{}",json!({"model_load_ms":engine.load_ms,"result":result}));
    Ok(())
}
