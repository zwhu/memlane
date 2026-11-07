//! Minimal raw-text path using pinned Cognee-RS ONNX + LanceDB interfaces.
//! Does not invoke cognify, extraction, summarization, improve or cloud providers.
use cognee::{
    cognee_embedding::{EmbeddingEngine, OnnxEmbeddingConfig, OnnxEmbeddingEngine, OnnxPooling},
    cognee_vector::{lancedb_adapter::LanceDbAdapter, VectorDB, VectorPoint},
    cognify::{HuggingFaceTokenCounter, TokenCounter},
};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{path::{Path, PathBuf}, sync::Arc, time::Instant};
use uuid::Uuid;

type Error = Box<dyn std::error::Error + Send + Sync>;
pub type Result<T> = std::result::Result<T, Error>;

#[derive(Deserialize, Serialize)]
pub struct Recipe {
    pub id: String,
    pub revision: String,
    pub dimension: usize,
    pub max_sequence_length: usize,
    pub batch_size: usize,
    pub pooling: String,
    pub query_instruction: String,
    #[serde(default)]
    pub passage_instruction: String,
    pub chunk_tokens: usize,
    pub overlap_tokens: usize,
    pub files: Value,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Record {
    pub dataset: Uuid,
    pub memory: Uuid,
    pub revision: u64,
    pub text: String,
}

#[derive(Serialize)]
pub struct Candidate {
    pub chunk_id: Uuid,
    pub dataset: Uuid,
    pub memory: Uuid,
    pub revision: u64,
    pub generation: String,
    pub text: String,
    pub start_byte: usize,
    pub end_byte: usize,
    pub score: f32,
}

pub struct Backend {
    root: PathBuf,
    pub recipe: Recipe,
    engine: OnnxEmbeddingEngine,
    counter: HuggingFaceTokenCounter,
    pub load_ms: f64,
}

impl Backend {
    pub fn new(root: PathBuf, model: &Path) -> Result<Self> {
        let recipe: Recipe = serde_json::from_str(&std::fs::read_to_string(model.join("recipe.json"))?)?;
        if recipe.batch_size == 0 || recipe.chunk_tokens == 0
            || recipe.overlap_tokens >= recipe.chunk_tokens
            || recipe.chunk_tokens + 32 > recipe.max_sequence_length {
            return Err("invalid chunk/model configuration".into());
        }
        let start = Instant::now();
        let counter = HuggingFaceTokenCounter::from_file(model.join("tokenizer.json"))?;
        let engine = OnnxEmbeddingEngine::new(OnnxEmbeddingConfig {
            model_path: model.join("model.onnx"), tokenizer_path: model.join("tokenizer.json"),
            model_name: recipe.id.clone(), dimensions: recipe.dimension,
            max_sequence_length: recipe.max_sequence_length, batch_size: recipe.batch_size,
            max_concurrent_data_points: recipe.batch_size,
            pooling: match recipe.pooling.as_str() {
                "cls" => OnnxPooling::Cls, "mean" => OnnxPooling::Mean,
                _ => return Err("unsupported pooling".into()),
            },
            query_instruction: Some(recipe.query_instruction.clone()),
        })?;
        std::fs::create_dir_all(&root)?;
        Ok(Self {root, recipe, engine, counter, load_ms: start.elapsed().as_secs_f64()*1000.0})
    }

    pub fn generation(&self) -> String {
        // Includes artifact hashes, both instructions, length and chunk recipe.
        // UUID also keeps untrusted recipe strings out of filesystem paths.
        let recipe=serde_json::to_vec(&self.recipe).expect("recipe contains only serializable fields");
        let mut identity=b"memlane-raw-v1:onnx-inputs-padding-v1:".to_vec();
        identity.extend(recipe);
        Uuid::new_v5(&Uuid::NAMESPACE_OID, &identity).to_string()
    }

    async fn vector(&self, dataset: Uuid) -> Result<LanceDbAdapter> {
        // Every call names an explicit UUID dataset and resolves an independent
        // directory. Never pass absent/all datasets or search then filter across projects.
        let db = LanceDbAdapter::new(self.root.join(dataset.to_string()).join(self.generation())).await?;
        db.create_collection("DocumentChunk", "text", self.recipe.dimension).await?;
        Ok(db)
    }

    pub fn spans(&self, text: &str) -> Result<Vec<(usize, usize)>> {
        if text.trim().is_empty() { return Err("empty record".into()); }
        let mut boundaries: Vec<usize> = text.char_indices().map(|(i,_)| i).collect();
        boundaries.push(text.len());
        let mut result = Vec::new();
        let mut start = 0;
        while start < boundaries.len()-1 {
            let mut low = start+1;
            let mut high = boundaries.len()-1;
            while low < high {
                let mid = (low+high+1)/2;
                if self.counter.count_tokens(&text[boundaries[start]..boundaries[mid]]) <= self.recipe.chunk_tokens {
                    low = mid;
                } else { high = mid-1; }
            }
            let end = low;
            if self.counter.count_tokens(&text[boundaries[start]..boundaries[end]]) > self.recipe.chunk_tokens {
                return Err("one character exceeds token budget".into());
            }
            result.push((boundaries[start], boundaries[end]));
            if end == boundaries.len()-1 { break; }
            let mut overlap = end;
            while overlap > start+1 && self.counter.count_tokens(&text[boundaries[overlap-1]..boundaries[end]]) <= self.recipe.overlap_tokens {
                overlap -= 1;
            }
            start = overlap.max(start+1);
        }
        Ok(result)
    }

    pub async fn index(self: &Arc<Self>, record: &Record) -> Result<Value> {
        if record.revision == 0 { return Err("revision must be positive".into()); }
        let start = Instant::now();
        let text=record.text.clone();
        let engine=Arc::clone(self);
        let spans = tokio::task::spawn_blocking(move || engine.spans(&text)).await??;
        let db = self.vector(record.dataset).await?;
        let document = Uuid::new_v5(&record.memory, format!("{}:{}:{}", record.dataset, record.revision, self.generation()).as_bytes());
        let mut ids = Vec::new();
        // Embed and commit bounded batches; no graph extraction is involved.
        for (number, (begin,end)) in spans.iter().enumerate() {
            let text = &record.text[*begin..*end];
            let prefixed = format!("{}{}", self.recipe.passage_instruction, text);
            if self.counter.count_tokens(&prefixed)+2 > self.recipe.max_sequence_length {
                return Err("chunk plus instruction exceeds model sequence limit".into());
            }
            let vector = self.engine.embed(&[&prefixed]).await?.remove(0);
            if vector.len() != self.recipe.dimension || vector.iter().any(|v| !v.is_finite()) {
                return Err("invalid embedding vector".into());
            }
            let id = Uuid::new_v5(&document, number.to_string().as_bytes());
            let point = VectorPoint::new(id, vector)
                .with_metadata("dataset", json!(record.dataset))
                .with_metadata("memory", json!(record.memory))
                .with_metadata("revision", json!(record.revision))
                .with_metadata("generation", json!(self.generation()))
                .with_metadata("text", json!(text))
                .with_metadata("start_byte", json!(begin))
                .with_metadata("end_byte", json!(end));
            db.index_points("DocumentChunk", "text", &[point]).await?;
            ids.push(id);
        }
        Ok(json!({"chunk_ids":ids,"generation":self.generation(),"chunks":spans.len(),
                  "index_ms":start.elapsed().as_secs_f64()*1000.0}))
    }

    pub async fn search(&self, dataset: Uuid, query: &str, top_k: usize) -> Result<Vec<Candidate>> {
        if query.trim().is_empty() || !(1..=1000).contains(&top_k) { return Err("invalid query or top_k".into()); }
        if self.counter.count_tokens(query) + 32 > self.recipe.max_sequence_length { return Err("query exceeds token limit".into()); }
        let db = self.vector(dataset).await?;
        let vector = self.engine.embed_query(query).await?.remove(0);
        let hits = db.search_similar("DocumentChunk", "text", &vector, top_k).await?;
        let mut out = Vec::new();
        for hit in hits {
            let get = |key: &str| -> Result<Value> { Ok(hit.metadata.get(key).ok_or("candidate provenance missing")?.clone()) };
            let scope: Uuid = serde_json::from_value(get("dataset")?)?;
            if scope != dataset { return Err("candidate dataset mismatch".into()); }
            out.push(Candidate {chunk_id:hit.id, dataset:scope,
                memory:serde_json::from_value(get("memory")?)?, revision:serde_json::from_value(get("revision")?)?,
                generation:serde_json::from_value(get("generation")?)?, text:serde_json::from_value(get("text")?)?,
                start_byte:serde_json::from_value(get("start_byte")?)?,end_byte:serde_json::from_value(get("end_byte")?)?,score:hit.score});
        }
        Ok(out)
    }

    pub async fn remove(&self, dataset: Uuid, ids: &[Uuid]) -> Result<()> {
        self.vector(dataset).await?.delete_points("DocumentChunk", "text", ids).await?;
        Ok(())
    }
}
