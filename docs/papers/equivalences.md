# Intra-Language Pattern Equivalences for Code Transformation

**Marcelo Cantos**

*April 2026*

---

## Abstract

Code transformation tools universally model refactoring as directional
rewriting: match a pattern, produce a replacement. This paper proposes
an alternative foundation for intra-language code transformation based
on *pattern equivalences* --- declarations that two syntactic patterns
are interchangeable within a codebase. A single equivalence declaration
subsumes bidirectional refactoring, convention enforcement, and
automated fixing. We ground this in a set-theoretic model where a
pattern over a grammar denotes the set of all ASTs it matches, and an
equivalence relates two such sets via shared placeholder bindings. We
show that equivalences compose transitively, forming a graph whose
paths represent derived transformations, and that the intra-language
restriction avoids the deep problems (type bridging, grammar extension,
semantic gaps) that make cross-language equivalences intractable in the
general case. We survey existing tools, develop the formal model,
examine placeholder semantics and composition properties, and identify
open problems. This work originates from ideas developed by Marcelo
Cantos in the context of the arr.ai project, where a broader vision of
cross-language transpilation as bidirectional set mappings was explored.

## 1. Introduction

A striking proportion of the refactoring budget in large codebases is
spent on migrations *within* a single language: replacing one logging
framework with another, adopting a new error-handling idiom, aligning
API call patterns with an updated SDK. These are not semantic changes
--- the program's behaviour is meant to be preserved. They are
syntactic migrations between equivalent patterns.

Yet the tools we reach for --- sed, cscope, IDE refactoring wizards,
and more recently Tree-sitter-based structural search --- all frame the
task as a one-way function: *find this, replace with that*. The user
must separately define the forward migration, the backward migration
(if rollback is needed), the lint rule (to detect violations), and the
autofix (to remediate them). These are four artefacts encoding the same
underlying fact: that two patterns are interchangeable.

What if we could state the fact once?

```
//python{logging.getLogger(${name}).${level}(${msg})}
<=>
//python{log.${level}(${msg}, logger=${name})}
```

This single declaration asserts that these two forms are equivalent in
our codebase. From it, a sufficiently capable tool can derive:

- **Forward refactoring**: rewrite left-hand matches to right-hand form.
- **Backward refactoring**: rewrite right-hand matches to left-hand form.
- **Convention enforcement**: flag all occurrences of the non-preferred form.
- **Automatic fixes**: every violation comes with a ready-made replacement.

This paper develops the theoretical foundations for this approach,
examines its practical implications, and maps out the open problems
that remain.

## 2. Background

### 2.1 Existing approaches

Several tools perform structural code transformations, but all of them
are fundamentally directional.

**Tree-sitter queries and transforms.** Tree-sitter provides a query
language over concrete syntax trees. Tools built on Tree-sitter (such
as Sawmill) use these queries to match AST patterns and apply
transformations. The model is match-then-act: a query finds nodes, and
a separate action specification describes what to do with them.

**Comby** (https://comby.dev) provides language-aware structural
search and replace using lightweight templates with holes. A Comby rule
has a `match` template and a `rewrite` template --- explicitly
directional. To migrate in reverse, one writes a second rule.

**Semgrep** (https://semgrep.dev) combines structural pattern matching
with static analysis for security and code quality. Rules specify a
`pattern` and optionally a `fix`, again directional. Semgrep's
strength is in its analysis capabilities (taint tracking, constant
propagation), but its transformation model is one-way.

**Coccinelle / SmPL** (https://coccinelle.lip6.fr) uses Semantic Patch
Language to describe transformations on C code. SmPL patches are
powerful --- they can express complex context-dependent changes with
`...` wildcards and position constraints --- but they are patches, not
equivalences. Each SmPL script describes a directed change.

**Stratego/XT** (https://www.metaborg.org/en/latest/background/stratego/index.html)
provides a full term-rewriting language with strategies for controlling
rule application order. Stratego rules are directional rewrite rules;
strategies compose them. While one can write both `A -> B` and `B -> A`
as separate rules, the system does not treat them as a single
equivalence.

**IDE refactorings** (rename, extract method, inline) are semantic
transformations built into specific IDEs. They are correct-by-construction
but narrow: each refactoring is a hand-coded procedure, not a
user-declared pattern.

The common thread is that every tool treats transformation as a
function: an input pattern maps to an output. The equivalence --- the
fact that two patterns are interchangeable --- is implicit at best,
split across multiple artefacts at worst.

### 2.2 The directional tax

The cost of directionality is not merely aesthetic. It creates practical
problems:

- **Duplication**: Forward and backward rules must be maintained
  separately. They can drift out of sync.
- **Convention rot**: A lint rule that flags pattern A but does not know
  about the equivalence with pattern B cannot generate fixes. A
  separate autofix must be written and kept consistent with the lint
  rule.
- **Migration planning**: When migrating between patterns, there is no
  systematic way to discover that A can reach C via B. Each hop is an
  independent rule.
- **Cognitive load**: Developers must reason about transforms as
  procedures rather than as declarations of fact.

## 3. The Equivalence Model

### 3.1 Patterns as sets

Let *G* be a context-free grammar (concretely, a Tree-sitter grammar
for a specific language). A *pattern* *P* over *G* is a template that
extends *G* with *placeholders* (also called *holes* or *metavariables*).
Each placeholder `${x}` stands for a subtree of the parse tree.

A pattern *P* with placeholders *x*_1, ..., *x*_*n* denotes a set of
ASTs:

> **AST**(*P*) = { *t* in **Trees**(*G*) | there exist subtrees *s*_1, ..., *s*_*n* such that *t* = *P*[*x*_1 := *s*_1, ..., *x*_*n* := *s*_*n*] }

That is, **AST**(*P*) is the set of all concrete syntax trees that can
be obtained by substituting valid subtrees for every placeholder in *P*.

A pattern with no placeholders denotes a singleton set (or the empty
set, if the literal is not valid in *G*). A pattern that is a single
placeholder `${x}` denotes the set of all trees in *G* (or all trees
of a given syntactic category, if the placeholder is typed).

### 3.2 Equivalence declarations

An *equivalence declaration* relates two patterns:

> *P* <=> *Q*

where *P* and *Q* are patterns over the same grammar *G*, sharing some
or all of their placeholders.

The declaration asserts: for any binding of the shared placeholders to
concrete subtrees, if the left-hand side produces a valid AST, then so
does the right-hand side, and vice versa. The two ASTs are considered
*equivalent* in whatever sense the declaring user intends (typically:
semantically equivalent in the codebase's context).

Formally, let **Var**(*P*) and **Var**(*Q*) be the placeholder sets. A
*binding* is a function *sigma* : **Var**(*P*) union **Var**(*Q*) -> **Subtrees**(*G*).
The equivalence holds under *sigma* when:

> *P*[*sigma*] is in **Trees**(*G*) if and only if *Q*[*sigma*] is in **Trees**(*G*)

The binding *sigma* is the *witness* of equivalence for a particular
instance. Given concrete values for the shared placeholders, both
patterns produce valid (and interchangeable) ASTs.

### 3.3 Properties

Equivalence declarations have the expected algebraic properties:

- **Reflexivity**: *P* <=> *P* is trivially valid for any pattern.
- **Symmetry**: If *P* <=> *Q*, then *Q* <=> *P*. This is immediate
  from the definition --- there is no distinguished direction.
- **Transitivity**: If *P* <=> *Q* and *Q* <=> *R*, then *P* <=> *R*,
  provided placeholder bindings can be composed. (This requires care;
  see Section 7.)

These properties mean that a collection of equivalence declarations
induces an equivalence relation on the set of all ASTs reachable
through pattern substitution.

### 3.4 The equivalence graph

Given a set of declared equivalences, we construct an *equivalence
graph*:

- **Nodes** are pattern-sets (each node represents a syntactic form).
- **Edges** are declared equivalences.
- **Paths** represent derived equivalences via transitivity.

This graph is the topology of the transformation space. A refactoring
from form *A* to form *C* is a path from *A* to *C* in the graph. A
convention is a designated "preferred" node; enforcement is the
assertion that all code should be at that node.

## 4. Applications

### 4.1 Bidirectional refactoring

The most immediate application: declare an equivalence, get both
directions for free.

**Logging migration** (Python):
```
//python{logging.getLogger(${name}).${level}(${msg})}
<=>
//python{structlog.get_logger(${name}).${level}(${msg})}
```

This single rule supports migrating from `logging` to `structlog` and
back. During an incremental migration, the team can move files in
either direction as needed.

**Error wrapping** (Go):
```
//go{fmt.Errorf("${msg}: %v", ${err})}
<=>
//go{fmt.Errorf("${msg}: %w", ${err})}
```

The shift from `%v` to `%w` in Go 1.13 enabled `errors.Is` and
`errors.As` to traverse wrapped errors. This is a case where the
equivalence is not fully symmetric in semantics --- `%w` preserves the
error chain while `%v` discards it --- but the mechanical migration is
well-understood, and the semantic implications are documentable in the
equivalence's rationale.

**Error handling** (Go):
```
//go{if err := ${expr}; err != nil { return ${zero}, err }}
<=>
//go{${zero}, err := errgroup.Try(func() (${T}, error) { return ${expr} })}
```

Here the equivalence is less clean --- the right-hand side introduces a
type parameter `${T}` not present on the left. This is a case where
placeholder semantics become interesting (see Section 6).

### 4.2 Convention enforcement

Declare the equivalence and mark one side as canonical:

```
//typescript{${obj}.forEach(${fn})}
<=>  [prefer right]
//typescript{for (const ${item} of ${obj}) { ${fn}(${item}) }}
```

The tool now has a lint rule (flag `forEach`) and an autofix (rewrite
to `for...of`) from a single declaration. Marking preference is
metadata on the equivalence, not a separate rule.

In a Sawmill context, an AI agent could request:

```json
{"tool": "apply_equivalence",
 "equivalence": "foreach-to-forof",
 "direction": "right",
 "scope": "src/"}
```

The same equivalence, with `"direction": "left"`, reverts the change.
The convention declaration makes the canonical direction the default.

### 4.3 Migration planning

Consider a codebase migrating through multiple API versions:

```
//python{requests.get(${url})}           <=>  //python{httpx.get(${url})}
//python{httpx.get(${url})}              <=>  //python{await httpx.AsyncClient().get(${url})}
//python{await httpx.AsyncClient().get(${url})}  <=>  //python{await session.get(${url})}
```

The equivalence graph now contains a path from `requests.get` to
`session.get`. The tool can:

1. Show the full migration path.
2. Estimate the number of intermediate steps.
3. Apply the migration incrementally (one hop at a time) or
   directly (composing the full chain).
4. Identify which files are at which stage of the migration.

### 4.4 Equivalence validation

A declared equivalence is an assertion by the user. The tool can help
validate it:

- **Test-based validation**: If the codebase has tests, apply the
  equivalence to a match site, run the tests, and check for regressions.
- **Static validation**: If the tool has access to type information
  (via LSP or similar), verify that both sides of the equivalence
  produce expressions of the same type.
- **Counterexample search**: Find match sites where the equivalence
  might not hold (e.g., where a placeholder binds to an expression
  with side effects, making reordering unsafe).

### 4.5 Library migration

Library version upgrades often involve mechanical API changes. When a
library publishes an equivalence set alongside its deprecation notices,
every consumer gets a migration path for free.

**Iterator simplification** (Rust):
```
//rust{${v}.iter().cloned().collect::<Vec<_>>()}
<=>
//rust{${v}.to_vec()}
```

A common Rust simplification. The equivalence holds when `${v}` is a
slice or `Vec` reference. A type constraint on the hole makes this
precise.

**Macro to operator** (Rust):
```
//rust{try!(${expr})}
<=>
//rust{${expr}?}
```

The `try!` macro to `?` operator migration was one of the largest
mechanical changes in Rust's history. An equivalence declaration
would have made it a one-liner.

**Optional chaining** (TypeScript):
```
//typescript{${obj} && ${obj}.${prop}}
<=>
//typescript{${obj}?.${prop}}
```

The `&&` guard pattern was pervasive before ES2020. An equivalence
declaration lets a codebase migrate incrementally, with the convention
preferring the right-hand form. Note the subtlety: the left-hand
pattern binds `${obj}` twice. The binding witness must verify that
both occurrences match the *same* subtree (syntactic identity, not
semantic equality).

**Lodash to native** (TypeScript):
```
//typescript{_.map(${arr}, ${fn})}
<=>
//typescript{${arr}.map(${fn})}
```

**f-strings** (Python):
```
//python{"{}".format(${x})}
<=>
//python{f"{${x}}"}
```

Every `str.format` call with positional arguments can be converted to
an f-string and back. The equivalence does not hold for computed format
strings (`template.format(x)` where `template` is a variable), so the
left-hand pattern is deliberately specific.

**Testing library** (TypeScript):
```
//typescript{enzyme.shallow(<${Component} ${...props} />).find(${selector})}
<=>
//typescript{render(<${Component} ${...props} />); screen.getByRole(${selector})}
```

This last example is approximate --- real testing library migrations
involve more context. But the pattern illustrates how equivalences can
encode the *shape* of a migration even when not every instance is
fully mechanical.

## 5. Placeholder Semantics

The power and the subtlety of the equivalence model both reside in the
placeholders. Several questions arise.

### 5.1 Typed vs untyped holes

In the simplest model, `${x}` matches any subtree. But grammars have
structure. Should `${x}` match:

- Any expression? (`${x:expr}`)
- Any statement? (`${x:stmt}`)
- Any identifier? (`${x:ident}`)
- Any sequence of statements? (`${x:stmts}`)
- Anything at all?

The answer affects both matching power and safety. An untyped hole in
an equivalence like `f(${x}) <=> g(${x})` is safe regardless of what
`${x}` is, because it appears in the same syntactic position on both
sides. But in `${x} + 0 <=> ${x}`, the hole must be an expression ---
matching a statement would produce nonsense.

A practical system likely needs at least expression-level and
statement-level holes, with identifier holes for renaming patterns.
Full type-system-aware holes (matching only expressions of type `int`,
for example) are desirable but require integration with type checkers
or LSP servers.

### 5.2 Structural constraints

Can a placeholder match a *sequence* of nodes? Consider:

```
//python{def ${name}(${params}): ${body}}
<=>
//python{${name} = lambda ${params}: ${body}}
```

Here `${params}` must match a parameter list (potentially multiple
parameters) and `${body}` must match a block of statements on the
left but a single expression on the right. This equivalence is only
valid when `${body}` is a single return statement whose expression can
serve as the lambda body.

This illustrates that some equivalences have *applicability conditions*
beyond pure syntactic matching. The system must either:

1. Restrict placeholders to prevent invalid matches.
2. Allow guards or conditions on equivalence declarations.
3. Accept that some declared equivalences are partial (valid for a
   subset of matches) and handle failures gracefully.

Option 3 is the most pragmatic starting point: attempt the
substitution, check whether the result parses, and report failures
rather than silently producing invalid code.

### 5.3 Side-effect preservation

Consider:

```
//python{print(${x}); print(${x})}
<=>
//python{msg = ${x}; print(msg); print(msg)}
```

If `${x}` is `get_timestamp()`, the left side calls it twice (getting
potentially different values) while the right side calls it once. The
equivalence is only valid when `${x}` is pure.

Side-effect analysis is generally undecidable, so a practical system
must either:

- Require the user to annotate when purity matters.
- Integrate with static analysis to flag likely violations.
- Treat all equivalences as best-effort and rely on testing.

### 5.4 Multiplicity and binding consistency

When a placeholder appears multiple times on one side:

```
//go{${x} == ${x}}  <=>  //go{true}
```

Both occurrences of `${x}` must bind to the *same* subtree (structural
equality of ASTs, not just string equality of source text). This is the
standard semantics for metavariables in term rewriting, but it
interacts with formatting: `x + 1` and `x+1` are the same AST but
different source strings. A Tree-sitter-based system naturally handles
this by comparing tree structure, not text.

Note also that this particular equivalence is wrong in general ---
`NaN == NaN` is `false` in IEEE 754 --- illustrating that declared
equivalences always carry semantic assumptions.

## 6. Composition and the Equivalence Graph

### 6.1 Transitive chains in practice

Given:

```
//python{${a} ** 2}     <=>  //python{${a} * ${a}}        (E1)
//python{${a} * ${a}}   <=>  //python{pow(${a}, 2)}       (E2)
//python{pow(${a}, 2)}  <=>  //python{math.pow(${a}, 2)}  (E3)
```

Transitivity gives us `${a} ** 2 <=> math.pow(${a}, 2)` via the
chain E1-E2-E3. The tool can rewrite `x ** 2` to `math.pow(x, 2)`
without any direct rule connecting these forms.

But note: E1 is only valid when `${a}` is pure (since the right side
evaluates `${a}` twice). This condition must propagate through the
chain --- the derived equivalence inherits the *conjunction* of all
conditions along the path.

### 6.2 Ambiguity

When multiple paths connect two nodes, which should the tool prefer?

```
A <=> B <=> D
A <=> C <=> D
```

Both paths rewrite A to D, but through different intermediate forms.
If A is `old_api.fetch(url)` and D is `new_api.request(url)`, the path
through B might preserve error handling semantics while the path through
C might not. The system needs a way to prefer one path --- shortest
path, user-annotated priority, or explicit path selection.

In practice, ambiguity is most likely when equivalences are contributed
by different teams or accumulated over time. A *lint-for-equivalences*
tool that detects and flags ambiguous paths would be valuable.

### 6.3 Canonicalisation

Given a connected component of the equivalence graph with a designated
"preferred" node, the system can automatically:

1. Identify all code matching *any* node in the component.
2. Rewrite all matches to the preferred node.
3. Report the changes as a single migration.

This is *canonicalisation*: collapsing an equivalence class to its
canonical representative. It generalises convention enforcement from
pairs to entire clusters of equivalent forms.

### 6.4 Confluence

Do different rewrite paths from A to D yield the same result? In
general, no --- the intermediate forms might introduce different
formatting, variable names, or structural choices. A confluent
equivalence system would guarantee that the final result is
independent of the path taken.

Confluence is well-studied in term rewriting (the Church-Rosser
property). For syntactic equivalences over a fixed grammar, confluence
holds when all equivalences are purely structural (no side conditions)
and placeholder bindings are deterministic. When conditions or
ambiguous bindings enter the picture, confluence must be verified
on a case-by-case basis or enforced by restricting the system.

For practical purposes, non-confluence is acceptable if the tool
always uses a deterministic path-selection strategy (e.g., shortest
path, then lexicographic tie-breaking). The user sees consistent
behaviour even if the theoretical system is non-confluent.

## 7. Relationship to the Cross-Language Case

The intra-language equivalence model is a degenerate --- and therefore
tractable --- case of a much broader vision.

### 7.1 The general case

In the arr.ai project, Cantos explored a grammar and macro system
(built on WBNF, a notation for defining arbitrary grammars) where
transformations between languages could be expressed as equivalences:

```
//python{${a} ** ${b}}  <=>  //c{powf(${a:float}, ${b:float})}
```

Here, the two sides use different grammars, different type systems,
and different AST representations. The placeholders must bridge these
gaps --- `${a}` on the Python side is an arbitrary expression, but on
the C side it must be a `float` expression. The equivalence is a
*typed relation* across grammar boundaries.

### 7.2 Why it is much harder

The cross-language case introduces problems that the intra-language
case avoids entirely:

**Grammar extension for placeholders.** Within a single language,
`${x}` can be parsed by temporarily extending the grammar with a
placeholder rule. Across languages, each side needs its own grammar
extension, and the placeholder syntax itself must not conflict with
either language's syntax.

**Type bridges.** Python's duck-typed `${a}` and C's `float`-typed
`${a:float}` represent fundamentally different things. A cross-language
equivalence system needs a type mapping layer that relates type systems
--- an open research problem in its own right.

**Semantic gaps.** Python integers have arbitrary precision; C integers
overflow. Python strings are Unicode; C strings are byte arrays.
Cross-language equivalences must account for these semantic differences
or explicitly declare their assumptions.

**Altitude mismatch.** A single line of Python might correspond to
dozens of lines of C (memory allocation, error checking, cleanup).
The granularity of patterns differs dramatically across languages.

### 7.3 Why intra-language is the practical sweet spot

Within a single language:

- Both sides share a grammar. No placeholder/sigil conflicts.
- Both sides share a type system. No semantic bridge needed.
- Both sides share an AST representation. Comparisons are trivial.
- Both sides share runtime semantics. Equivalence assertions are
  easier to validate.
- Transitivity composes naturally --- all intermediate forms live in
  the same grammar.

This makes intra-language equivalences immediately implementable with
existing parsing technology (Tree-sitter, Comby-style templates) and
immediately useful for the most common refactoring tasks.

## 8. Open Questions

### 8.1 Semantic preservation

When is a declared equivalence actually semantics-preserving? The
system accepts equivalences as user assertions, but validation is
crucial for safety. Possible approaches:

- **Property-based testing**: Generate random inputs, apply the
  equivalence, run both versions, compare outputs.
- **Symbolic execution**: Prove equivalence for all inputs within
  a bounded scope.
- **Type-directed validation**: Verify that both sides have the same
  type, and that reordering of subexpressions does not violate
  evaluation-order dependencies.

None of these are complete. Semantic equivalence of programs is
undecidable in general (Rice's theorem). The practical question is how
much confidence can be achieved with reasonable effort.

### 8.2 Context-dependent equivalences

Some equivalences are valid only in certain contexts:

```
//python{await ${x}}  <=>  //python{asyncio.run(${x})}
```

This is valid at the top level but not inside an `async def` (where
`await` is the correct form). The equivalence needs a context
constraint: "only match when not inside an async function."

How should context constraints be expressed? Options include:

- Negative structural patterns ("not inside `async def`").
- Scope annotations ("only in module scope").
- Explicit guard expressions.

### 8.3 Placeholder granularity

What is the right set of placeholder kinds? The spectrum runs from
fine-grained (individual tokens) to coarse (arbitrary subtrees):

| Granularity | Example | Use case |
|---|---|---|
| Token | `${op:binary_operator}` | Operator substitution |
| Identifier | `${name:ident}` | Renaming |
| Expression | `${x:expr}` | Most refactorings |
| Statement | `${s:stmt}` | Control flow changes |
| Block | `${body:block}` | Function body transforms |
| Sequence | `${stmts:stmt*}` | Multi-statement patterns |

A minimal viable system needs expression and identifier holes. A
practical system probably needs all of these. The question is whether
the type of a placeholder should be inferred from its syntactic
position in the pattern (likely, and sufficient for most cases) or
explicitly annotated (needed for ambiguous positions).

### 8.4 Inference from examples

Can equivalences be *inferred* rather than declared? Given a test
suite and a set of before/after code pairs (e.g., from a pull request),
could a tool infer the equivalence rule that explains the change?

This is a form of *programming by example* applied to code
transformation. Anti-unification (finding the least general
generalisation of two terms) is a well-studied technique that could
serve as a starting point. The challenge is that real refactorings
often involve more context than a single pattern pair captures.

### 8.5 Interaction with type systems

Gradual typing (as in Python with type hints, or TypeScript) creates
interesting interactions. An equivalence might be valid for dynamically
typed code but not for statically typed code (or vice versa). For
example:

```
//typescript{${x} as ${T}}  <=>  //typescript{<${T}>${x}}
```

This equivalence between `as`-casts and angle-bracket casts is valid
in TypeScript, but the angle-bracket form is forbidden in `.tsx` files
(where it conflicts with JSX syntax). File-type context matters.

More broadly, typed holes (`${x:number}`) require type resolution,
which may depend on imports, type inference, and the surrounding
scope. Integrating equivalence checking with language servers (LSP)
is a natural path but adds significant complexity.

### 8.6 Equivalence maintenance

As a language evolves, equivalences may become invalid. Rust's edition
system, Python's `__future__` imports, and TypeScript's `strict` mode
all change the set of valid equivalences. An equivalence valid under
Python 3.9 (walrus operator patterns) may not be applicable to a
codebase still on 3.7.

Equivalences should carry version or edition metadata:

```
//rust{try!(${expr})} <=> //rust{${expr}?}
  [editions: 2015..2018]
```

A tool should prune inapplicable equivalences based on project
configuration (Cargo.toml edition, pyproject.toml python-requires,
tsconfig.json target). Stale equivalences --- those referencing
deprecated syntax that is no longer parseable --- should be flagged
for removal.

### 8.7 Scale and performance

In a large codebase with hundreds of declared equivalences, the
equivalence graph could become complex. Questions include:

- How expensive is it to compute all match sites for a pattern?
  (Tree-sitter queries are fast, but the number of patterns scales
  with the number of equivalences.)
- How expensive is transitive-closure computation on the graph?
- Can the graph be incrementally maintained as files change?

For the intra-language case with Tree-sitter, matching is fast
(Tree-sitter queries run in milliseconds even on large files). The
graph operations are over the number of *declared equivalences*, not
the number of AST nodes, so they should remain tractable for any
realistic number of declarations.

## 9. Conclusion

The intra-language pattern equivalence model is a tractable extraction
from a deeper theoretical programme. By constraining both sides of an
equivalence to the same language, we avoid the hardest problems
(grammar bridging, type mapping, semantic gaps) while retaining most
of the practical value.

The core insight is a reframing: a code transformation tool should not
be a function executor that maps patterns to replacements. It should be
a *navigator* through an equivalence space defined by user declarations.
The topology of this space --- which patterns can reach which other
patterns, through which intermediate forms --- is as important as any
individual transformation.

This reframing has immediate practical consequences. A single
equivalence declaration replaces four separate artefacts (forward
rule, backward rule, lint check, autofix). Transitivity enables
multi-hop migrations that no individual rule can express. The
equivalence graph provides a map of the migration landscape that
developers can inspect and reason about.

The open problems are real. Semantic preservation is undecidable in
general and requires practical approximations. Context-dependent
equivalences need an expressive constraint language. Placeholder
semantics must balance power against safety. Confluence is not
guaranteed.

But these are research problems worth solving, because the directional
model we have today imposes a concrete tax on every refactoring effort
in every codebase. The equivalence model offers a path toward tools
that understand what developers already know: that refactoring is
not about transforming code in one direction, but about recognising
that two forms are the same thing, and choosing which one to keep.

---

*This work originates from ideas developed by Marcelo Cantos in the
context of the arr.ai project, where a broader vision of cross-language
transpilation as bidirectional set mappings was explored within WBNF, a
notation for defining arbitrary grammars with macro-level bridging
between language boundaries.*

---

## References

1. M. Cantos. *arr.ai: A general-purpose data-oriented language*.
   https://github.com/arr-ai/arrai

2. M. Cantos. *WBNF: A notation for defining grammars*.
   https://github.com/arr-ai/wbnf

3. R. van Tonder and C. Le Goues. "Lightweight Multi-Language Syntax
   Transformation with Parser Parser Combinators." *PLDI 2019*.
   (Comby: https://comby.dev)

4. Y. Padioleau. "Parsing C/C++ Code without Pre-processing." *CC 2009*.
   (Coccinelle/SmPL: https://coccinelle.lip6.fr)

5. Semgrep. *Lightweight static analysis for many languages*.
   https://semgrep.dev

6. E. Visser. "Stratego: A Language for Program Transformation Based on
   Rewriting Strategies." *RTA 2001*.

7. M. Brunel et al. *Tree-sitter: An incremental parsing system for
   programming tools*. https://tree-sitter.github.io/tree-sitter/

8. G. Plotkin. "A Note on Inductive Generalization." *Machine
   Intelligence 5*, 1970. (Anti-unification)

9. F. Baader and T. Nipkow. *Term Rewriting and All That*. Cambridge
   University Press, 1998. (Confluence, Church-Rosser property)

10. M. Cantos. *Sawmill: MCP server for AST-level multi-language code
    transformations*. https://github.com/marcelocantos/sawmill
