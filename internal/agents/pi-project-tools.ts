// Host-authenticated, read-only tools for exactly one frozen repository task.
import { Type } from "typebox";
const projectEndpoint = __MEMLANE_PROJECT_ENDPOINT__;
const projectToken = __MEMLANE_PROJECT_TOKEN__;
const projectToolNames = ["memlane_project_tree", "memlane_project_source", "memlane_project_knowledge", "memlane_project_finish"];
function installProjectTools(pi, onFinish) {
  const definitions = [
    ["memlane_project_tree", "List one authorized repository directory. Root uses an empty path. Further directories must come from this listing; do not probe invented names. No symlinks, sessions, credentials or environment files.", Type.Object({path: Type.Optional(Type.String())})],
    ["memlane_project_source", "Read and freeze an exact repository file named by tree. First finish reading current knowledge. Cite the returned source id and exact quote.", Type.Object({path: Type.String()})],
    ["memlane_project_knowledge", "Read current effective knowledge in this project. Begin with empty cursor and follow every next cursor; do not duplicate or overwrite existing decisions.", Type.Object({cursor: Type.String()})],
    ["memlane_project_finish", "End repository/context reads and produce the final JSON proposal. Call only after examining the repository and all current knowledge. Tools cannot be used afterward.", Type.Object({})],
  ];
  for (const [name, description, parameters] of definitions) {
    pi.registerTool({name, label: name, description, parameters,
      async execute(_id, parameters, signal) {
        const response = await fetch(projectEndpoint, {method: "POST",
          headers: {"Content-Type": "application/json", Authorization: `Bearer ${projectToken}`},
          body: JSON.stringify({...parameters, tool: name}), signal});
        const text = await response.text();
        if (!response.ok) return {isError: true, content: [{type: "text", text}], details: {ok: false}};
        const result = JSON.parse(text);
        if (name === "memlane_project_finish") {
          if (result.ready !== true || result.read_phase_ended !== true) throw new Error("project read barrier missing");
          pi.setActiveTools([]); onFinish();
        }
        return {content: [{type: "text", text}], details: {ok: true}};
      }
    });
  }
  pi.on("session_start", () => pi.setActiveTools(projectToolNames));
}
