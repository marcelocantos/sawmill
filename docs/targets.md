# Convergence Targets

## 🎯T1 Phase 2: Match/act engine + multi-language + querying

**Status:** ACHIEVED

## 🎯T2 Phase 3: Programmable transforms + batch + named ops

**Status:** In progress

**Desired state:** Agents can supply JavaScript transform functions
that run in an embedded QuickJS sandbox, compose multiple transforms
into a single diff via `transform_batch`, and use higher-level named
operations for common refactorings.

### Sub-targets

- 🎯T2.1 **Embedded QuickJS** — `transform_fn` parameter on the
  `transform` tool. JS function receives `TransformNode` objects,
  returns mutations. Sandboxed, no filesystem/network.
- 🎯T2.2 **`transform_batch`** — sequence of transforms (named ops
  + match/act) applied in order, producing a single diff.
- 🎯T2.3 **Named operations** — `add_parameter`, `remove_parameter`
  as MCP tools. (`extract_function`, `inline`, `move_symbol` deferred
  — they require deeper semantic analysis.)
- 🎯T2.4 **Cross-file reference tracking** — basic symbol index for
  rename/find_references across files with import awareness.
