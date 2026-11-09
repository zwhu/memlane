// The sole trusted background Extension. No tools, approval or database access.
import { openAIResponsesApi } from "@earendil-works/pi-ai";
const schema = __MEMLANE_OUTPUT_SCHEMA__;
const schemaDigest = "__MEMLANE_SCHEMA_DIGEST__";
const provider = "__MEMLANE_SCHEMA_PROVIDER__";
export default function (pi) {
  if (provider === "deepseek") {
    // Chat Completions only offers json_object on DeepSeek. Adapt the stream
    // inside this ephemeral pi process; retain its model, endpoint and auth.
    // No models, credentials or provider settings are registered/replaced.
    const responses = openAIResponsesApi();
    pi.registerProvider(provider, {
      api: "openai-completions",
      streamSimple(model, context, options) {
        return responses.streamSimple({...model, api: "openai-responses"}, context, options);
      }
    });
  }
  pi.on("before_provider_request", (event, ctx) => {
    if (ctx.model?.provider !== provider || ctx.model?.api !== "openai-completions") {
      throw new Error("Memlane native schema binding requires the selected adapted completion API");
    }
    const payload = event.payload;
    if (!payload || typeof payload !== "object" || Array.isArray(payload) ||
        (Array.isArray(payload.tools) && payload.tools.length !== 0)) {
      throw new Error("Memlane background payload must have no tools");
    }
    process.stdout.write(JSON.stringify({type: "memlane_schema_bound", schema_sha256: schemaDigest}) + "\n");
    if (provider === "deepseek") {
      if (!Array.isArray(payload.input) || "messages" in payload) {
        throw new Error("Memlane DeepSeek schema requires the Responses request protocol");
      }
      return {...payload, text: {format: {type: "json_schema", name: "memlane_organization", schema}}};
    }
    return {...payload, response_format: {type: "json_schema", json_schema: {
      name: "memlane_organization", strict: true, schema
    }}};
  });
  pi.on("after_provider_response", (event) => {
    process.stdout.write(JSON.stringify({type: "memlane_schema_response", schema_sha256: schemaDigest,
      status: event.status}) + "\n");
  });
}
