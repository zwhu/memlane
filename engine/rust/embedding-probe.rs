//! P0 probe: calls the pinned Cognee-RS ONNX provider on local model files.
//! No Cognee global config, dotenv, cloud provider or automatic model download.
use cognee::cognee_embedding::{EmbeddingEngine, OnnxEmbeddingConfig, OnnxEmbeddingEngine, OnnxPooling};
use serde::Deserialize;
use serde_json::json;
use std::{io::Read, path::PathBuf, time::Instant};

#[derive(Deserialize)]
struct Input {
    passages: Vec<String>,
    queries: Vec<String>,
}

#[derive(Deserialize)]
struct Recipe {
    id: String,
    dimension: usize,
    max_sequence_length: usize,
    batch_size: usize,
    pooling: String,
    query_instruction: String,
    #[serde(default)]
    passage_instruction: String,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let dir = PathBuf::from(std::env::args().nth(1).ok_or("expected model directory")?);
    let mut raw = String::new();
    std::io::stdin().read_to_string(&mut raw)?;
    let input: Input = serde_json::from_str(&raw)?;
    let recipe: Recipe = serde_json::from_str(&std::fs::read_to_string(dir.join("recipe.json"))?)?;
    let start = Instant::now();
    let engine = OnnxEmbeddingEngine::new(OnnxEmbeddingConfig {
        model_path: dir.join("model.onnx"),
        tokenizer_path: dir.join("tokenizer.json"),
        model_name: recipe.id,
        dimensions: recipe.dimension,
        max_sequence_length: recipe.max_sequence_length,
        batch_size: recipe.batch_size,
        max_concurrent_data_points: 1,
        pooling: match recipe.pooling.as_str() {
            "cls" => OnnxPooling::Cls,
            "mean" => OnnxPooling::Mean,
            _ => return Err("unsupported pooling".into()),
        },
        query_instruction: Some(recipe.query_instruction),
    })?;
    let load_ms = start.elapsed().as_secs_f64() * 1000.0;
    let start = Instant::now();
    let prefixed: Vec<String> = input.passages.iter().map(|p| format!("{}{}", recipe.passage_instruction, p)).collect();
    let passages: Vec<&str> = prefixed.iter().map(String::as_str).collect();
    let vectors = engine.embed(&passages).await?;
    let passage_ms = start.elapsed().as_secs_f64() * 1000.0;
    let queries: Vec<&str> = input.queries.iter().map(String::as_str).collect();
    let start = Instant::now();
    let query_vectors = engine.embed_queries(&queries).await?;
    println!("{}", json!({"engine": "cognee-rs", "dimension": engine.dimension(),
        "model_load_ms": load_ms, "passage_ms": passage_ms,
        "query_ms": start.elapsed().as_secs_f64() * 1000.0,
        "passage_vectors": vectors, "query_vectors": query_vectors}));
    Ok(())
}
