// The sole trusted background Extension. No tools, approval or database access.
const schema = __MEMLANE_OUTPUT_SCHEMA__;
const schemaDigest = "__MEMLANE_SCHEMA_DIGEST__";
export default function (pi) {
  pi.on("before_provider_request", (event, ctx) => {
    if (ctx.model?.provider !== "ollama" || ctx.model?.api !== "openai-completions") {
      throw new Error("Memlane native schema binding requires the adapted Ollama API");
    }
    const payload = event.payload;
    if (!payload || typeof payload !== "object" || Array.isArray(payload) ||
        (Array.isArray(payload.tools) && payload.tools.length !== 0)) {
      throw new Error("Memlane background payload must have no tools");
    }
    process.stdout.write(JSON.stringify({type: "memlane_schema_bound", schema_sha256: schemaDigest}) + "\n");
    return {...payload, response_format: {type: "json_schema", json_schema: {
      name: "memlane_organization", strict: true, schema
    }}};
  });
  pi.on("after_provider_response", (event) => {
    process.stdout.write(JSON.stringify({type: "memlane_schema_response", schema_sha256: schemaDigest,
      status: event.status}) + "\n");
  });
}
