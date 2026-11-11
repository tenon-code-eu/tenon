---
date: 2026-11-11
slug: model-proposes-harness-disposes
description: A mathematical reading of tenon. The language model is an untrusted oracle; every guarantee lives in the harness that filters its actions. Transition systems, decision orders, information-flow labels, hash chains, hysteresis and reproducible builds.
---

# The Model Proposes, the Harness Disposes

*A theory of tenon's invariants.*

## Abstract

A coding agent is usually described by what its model can do. This essay describes tenon by what its model **cannot** do. We treat the language model as an untrusted oracle: a function that may return any sequence of tool calls, including adversarial ones. Every guarantee tenon offers must then be a property of the *harness*, the deterministic program that sits between the oracle and the world. We model the harness as a guarded transition system and state its invariants precisely: a total order on decisions and a monotonicity result for organisation policy; an information-flow label that can only rise; a consistent redaction map whose markers can never be written back; a hash chain whose tampering reduces to a collision in SHA-256; a hysteresis loop that bounds the context sent to the model; a termination bound for every turn; and a supply chain where a binary is a deterministic function of its source. Each statement is checked against the code it describes, with file references. We close with what is *not* proved.

<!-- more -->

## Contents

1. [The harness as a transition system](#1-the-harness-as-a-transition-system)
2. [Decisions: an order, not a vote](#2-decisions-an-order-not-a-vote)
3. [Configuration as a semilattice](#3-configuration-as-a-semilattice)
4. [Sovereignty as information flow](#4-sovereignty-as-information-flow)
5. [Redaction: a consistent, one-way map](#5-redaction-a-consistent-one-way-map)
6. [Confinement: shrinking the reachable set](#6-confinement-shrinking-the-reachable-set)
7. [The audit chain](#7-the-audit-chain)
8. [Context as a resource: hysteresis and compaction](#8-context-as-a-resource-hysteresis-and-compaction)
9. [Termination and rewind](#9-termination-and-rewind)
10. [The supply chain as a function](#10-the-supply-chain-as-a-function)
11. [What is not proved](#11-what-is-not-proved)

References · Notes

## Prelude

"Man proposes, God disposes" is a sentence about who has the last word. In an agent, the model writes the proposal: *read this file*, *run this command*, *send this context*. Something else decides whether the proposal becomes an effect. In tenon that something is about forty Go packages under `internal/`, and the interesting question is not how clever the model is but what remains true **whatever** the model says.

That question has a long history. Saltzer and Schroeder's design principles[^ss] (fail-safe defaults, complete mediation, least privilege) are exactly the principles of a harness: every action goes through one checkpoint, the default answer is no, and each tool gets only the rights it needs. What follows is an attempt to write those principles down for one concrete program, as definitions and propositions rather than slogans.

## 1. The harness as a transition system

!!! definition "Definition 1 (State)"
    A session state is a tuple

    $$
    \sigma = (H,\; \Phi,\; A,\; \ell,\; u)
    $$

    where $H$ is the conversation history (a list of messages, with the system prompt $H_0$), $\Phi$ is the file system as seen from the project root, $A = (a_1,\dots,a_n)$ is the audit chain, $\ell \in \{0,1\}$ is the sensitivity label of the session, and $u \in \mathbb{N}$ is the number of tokens used so far.

The model is a function $\mathcal{M}$ from a *view* of the history to a message that may contain tool calls $c_1,\dots,c_k$. We make no assumption about $\mathcal{M}$: it may be wrong, manipulated by a prompt injection in a file it read, or simply unlucky. The harness is the transition function

$$
\delta(\sigma, c) =
\begin{cases}
\sigma' & \text{if } \mathrm{guard}(\sigma, c) \text{ holds and the tool runs,}\\
\sigma \oplus \mathrm{refusal}(c) & \text{otherwise,}
\end{cases}
$$

where $\oplus$ appends a tool message explaining the refusal, and *every* branch appends an audit record.

The guard is not one test but a pipeline, executed for each call in the order the model emitted them, sequentially, never in parallel (`internal/engine/loop.go`):

1. **argument check**: write tools and MCP tools refuse arguments that contain a redaction marker (§5);
2. **pre-tool hooks**, which may block, ask, or rewrite the arguments (rewritten arguments are re-validated);
3. **permission decision** $d(c) \in \mathcal{D}$ (§2);
4. **human approval**, if $d(c)$ requires it;
5. **classification gate** on the final arguments (§4);
6. **execution**, confined by the sandbox (§6).

The point of writing it this way is the shape of the safety argument. A *safety property* in the sense of Lamport[^lamport] is a set of executions closed under prefixes: "nothing bad ever happens". For such properties it is enough to show that $\delta$ preserves an invariant $I$:

$$
I(\sigma) \;\wedge\; \sigma \xrightarrow{c} \sigma' \;\Longrightarrow\; I(\sigma') \qquad \text{for every } c.
$$

The quantifier is over **every** call, which is precisely what lets us ignore $\mathcal{M}$. Each section below names one such $I$.

## 2. Decisions: an order, not a vote

A rule is a tuple $r = (\text{tool}, \text{pattern}, \text{decision}, \text{project}, \text{network})$, where the pattern is a wildcard in which `*` matches any substring. Rules come from three places: the user's global configuration, the project's `.tenon/config.toml`, and the signed organisation policy.

!!! definition "Definition 2 (Decision order)"
    Let

    $$
    \mathcal{D} = \{\,\mathsf{allow} \prec \mathsf{ask}^{\uparrow} \prec \mathsf{ask} \prec \mathsf{deny}\,\}
    $$

    where $\mathsf{ask}^{\uparrow}$ is a *liftable* question (a hook may answer it) and $\mathsf{ask}$ is a question only a human can answer.

The decision function (`DecideNet`, `internal/policy/permissions.go`) is not a majority vote over rules. It is a cascade of guards, the first of which to fire wins:

$$
d(c) =
\begin{cases}
\mathsf{deny} & \exists\, r \in R_{\mathsf{deny}} : r \models c \\
\mathsf{ask} & c \text{ is forced (non-EU MCP server)} \\
\mathsf{ask} & \exists\, p \in \mathrm{paths}(c) : p \notin \mathrm{root} \;\vee\; \mathrm{sensitive}(p) \\
\mathsf{ask} & \exists\, r \in R_{\mathsf{ask}}^{\mathrm{proj}} : r \models c \\
\mathsf{allow} & \exists\, r \in R_{\mathsf{allow}} : r \models_{\forall} c \\
\mathsf{ask}^{\uparrow} & \exists\, r \in R_{\mathsf{ask}} : r \models c \\
\mathsf{allow} & \mathrm{risk}(c) = \mathrm{Read} \\
\mathsf{ask}^{\uparrow} & \text{otherwise.}
\end{cases}
$$

Three asymmetries are encoded here and are worth stating.

*Matching is asymmetric.* A deny or ask rule matches if **one** path of the call matches ($\models$); an allow rule must match **all** of them ($\models_\forall$). Deny is also compared case-insensitively and against every form of the path (lexical, resolved through symlinks, raw), so that a link cannot hide a denied target. Allow is compared only against the resolved form.

*Allow does not compose with the shell.* An allow pattern on a command never covers a command containing a shell metacharacter (`;`, `&`, `|`, a backtick, `$(`, `>`, `<` or a newline) that the pattern itself does not contain (`chainsBeyond`). Without this, `allow "git status*"` would allow `git status; curl …`. The wildcard is a language; this check ensures that the language of an allow rule is not silently closed under sequential composition.

*The default depends on risk.* Each tool has a risk class in $\{\mathrm{Read}, \mathrm{Write}, \mathrm{Exec}\}$. Reading inside the project is allowed by default; writing and executing are asked by default. This is Saltzer and Schroeder's *fail-safe default* made literal.

Approval itself is a function too. A hook's *allow* can lift only $\mathsf{ask}^{\uparrow}$, never $\mathsf{ask}$ nor $\mathsf{deny}$; a hook's *ask* forces a question even over $\mathsf{allow}$. A human's "yes for the session" is remembered under the key $(\text{tool}, \text{command}, \text{paths})$, but **never** for network access, which must be granted again each time.

## 3. Configuration as a semilattice

The organisation policy is meant to be a *floor*: something a user cannot loosen. "Cannot loosen" is a monotonicity statement, and it can be proved.

The policy file is verified before anything else: it must be signed with minisign (Ed25519[^ed25519]), and on a system directory every file must be owned by root and not writable by group or others; otherwise tenon refuses to start. Its rules may only be `deny` or `ask` (`internal/orgpolicy/policy.go` rejects `"allow" rules`), and they are inserted **before** all others with the project flag set.

!!! proposition "Proposition 1 (The policy is a floor)"
    Let $P$ be a set of policy rules, all in $\{\mathsf{deny}, \mathsf{ask}\}$ and flagged *project*, and let $U$ be any set of user rules, including allows. For every call $c$, if some rule of $P$ matches $c$, then

    $$
    d(P \cup U,\; c) \;\succeq\; \mathsf{ask}.
    $$

!!! proof "Proof"
    If a deny rule of $P$ matches, the first case of the cascade fires and the result is $\mathsf{deny}$, whatever $U$ contains. Otherwise an ask rule of $P$ matches. Every case before the fourth returns $\mathsf{deny}$ or $\mathsf{ask}$. The fourth case, project asks, fires on that rule of $P$ and returns $\mathsf{ask}$. The allow case is fifth, so no rule of $U$ is ever consulted for an allow. In every branch the result is at least $\mathsf{ask}$.

The same idea extends to the whole configuration. Let each setting live in a finite chain ordered by strictness: compliance profiles

$$
\texttt{none} < \texttt{eu-only} < \texttt{secnumcloud} < \texttt{strict-local},
$$

sandbox modes $\texttt{off} < \texttt{auto} < \texttt{required}$, a token budget where *smaller is stricter*, memory on or off, and so on. A configuration is a point in the product $\mathcal{C} = \prod_i C_i$ of these chains, ordered componentwise. The product of chains is a lattice, and tenon merges the global file $g$, the project file $p$ and the policy $\pi$ with the join:

$$
\mathrm{cfg} = g \sqcup p \sqcup \pi.
$$

In the code this is `Stricter` for profiles, "lowest non-zero" for budgets, "strictest of all" for the sandbox, and *append* for deny-read paths, redaction rules and classification markers (sets ordered by inclusion). The project file is further restricted syntactically: it may not contain allow rules, network grants, a sandbox `off`, or extra writable paths.

!!! proposition "Proposition 2 (Merging never loosens)"
    $\mathrm{cfg} \succeq g$, $\mathrm{cfg} \succeq p$ and $\mathrm{cfg} \succeq \pi$. In particular a cloned repository can make tenon stricter on your machine, never looser; and a user can make it stricter than the organisation, never looser.

This follows from the definition of a join. Its practical value is that one does not have to reason about the *order* in which files are loaded: $\sqcup$ is commutative and associative.

## 4. Sovereignty as information flow

Data residency is a statement about where information may flow, and information flow has had a mathematical theory since Denning's lattice model[^denning]: give every object a security class, give every channel a clearance, and allow a flow only upward.

tenon uses the smallest non-trivial instance of that model, a two-point lattice $\{0 \sqsubset 1\}$ for the session label $\ell$, and a jurisdiction for every provider:

$$
j(p) \in \{\texttt{local},\; \texttt{eu},\; \texttt{non-eu}\}.
$$

!!! definition "Definition 3 (Classification)"
    A file path matching a configured classified pattern, or a text containing a marker such as `DIFFUSION RESTREINTE`, `SPÉCIAL FRANCE`, `RESTREINT UE` or `EU RESTRICTED` (compared after folding case, accents and whitespace, on word boundaries), is *classified*. Touching a classified object sets $\ell \leftarrow 1$.

Two invariants follow from `internal/engine/classify.go` and `internal/routing`.

!!! proposition "Proposition 3 (The label only rises)"
    For every transition $\sigma \to \sigma'$, $\ell' \ge \ell$.

There is no code path that resets $\ell$ within a session; the `SessionClassified` event is emitted once. This is Denning's *high-water mark*, and its monotonicity is what makes it safe: a session cannot "forget" that it saw a classified document and then talk to a remote model.

!!! proposition "Proposition 4 (No upward leak)"
    Every request sent to a provider $p$ satisfies

    $$
    \ell = 1 \;\Longrightarrow\; j(p) = \texttt{local}.
    $$

The router computes its candidate list from $\ell$: a sensitive session gets the configured local provider **only**, and a candidate declared local whose jurisdiction is not `local` is dropped. If no local provider exists, the harness refuses instead of degrading: classified path calls are denied, marked tool results are replaced by `[classified content withheld: no local provider]`, and a marked prompt is blocked. As a second line, before every send to a non-local provider, the raw and the redacted request are both scanned for markers.

The rest of residency is a conjunction of predicates evaluated before **each** send (`Outbound.Check`):

$$
\mathrm{send}(p) \iff \mathrm{profile}(p) \wedge \mathrm{policyHosts}(p) \wedge \bigwedge_{k} \mathrm{allowlist}_k(p) \wedge \big(j(p) \in \{\texttt{eu},\texttt{local}\} \vee \mathrm{consent}(p)\big).
$$

The order matters: consent appears only in the last conjunct, so a user's "yes, send it outside the EU" can never override a profile, a policy or an allowlist. And a jurisdiction that is not declared defaults to `non-eu`: the unknown is treated as the worst case.

Routing has one more property, less obvious. When the first provider fails, tenon may fall back to the next candidate, but only **before the first useful chunk** of the answer has arrived, and only for errors classified as retryable (no first chunk within 60 s, connection refused, unavailable). Hence:

!!! proposition "Proposition 5 (No spliced answers)"
    Every assistant message in $H$ was produced by exactly one provider.

An answer is never the concatenation of a prefix from one model and a suffix from another, which would make the audit record of "which model said this" meaningless.

## 5. Redaction: a consistent, one-way map

Before anything leaves the machine (model requests, tool arguments, audit lines, session files, headless output) it passes through a redactor $\mu$ (`internal/policy/redact.go`). Its rules recognise private keys, cloud and forge tokens, JWTs, passwords in URLs, keyword assignments such as `api_key = …` with a value of at least 12 characters, and some personal data: e-mail addresses, French phone numbers, IBANs and French social security numbers (NIR).

For the last two, recognition is not a regular expression alone but a checksum, which keeps false positives rare. An IBAN is accepted when its rearranged digits satisfy ISO 7064:

$$
\mathrm{IBAN} \equiv 1 \pmod{97},
$$

and a NIR $n$ (13 digits, with Corsica's `2A`/`2B` mapped to 19 and 18) carries the key

$$
k = 97 - (n \bmod 97).
$$

!!! definition "Definition 4 (Marker map)"
    The redactor is stateful within a session. For a recognised value $v$ of type $T$, it returns a marker $\mu(v) = \texttt{[}T\texttt{\_}i\texttt{]}$, where $i$ is a counter per type, and it remembers the pair.

The map is **consistent**: $v = v' \Rightarrow \mu(v) = \mu(v')$, so the model can still reason about "the same key used twice". It is **injective** on recognised values: two different secrets get two different markers. And it is **one-way**: there is no inverse anywhere in the code. Markers are never restored, not even in the user's own session files.

One-wayness creates a hazard: the model, seeing `[EMAIL_1]`, might write it into a file and corrupt the project. The harness closes that loop.

!!! proposition "Proposition 6 (Markers are never written back)"
    No write tool and no MCP tool runs with an argument that contains a match of the marker language $L_\mu$, the regular expression `\[[A-Z_]+_\d+\]`, including inside JSON strings nested in the arguments.

The check runs first in the guard pipeline, before hooks and permissions, so it holds even for calls a human would have approved.

## 6. Confinement: shrinking the reachable set

Permissions decide *whether* a command runs. The sandbox decides what it can touch *once it runs*, and it does not trust the command to tell the truth about itself. On macOS tenon generates a Seatbelt profile for `sandbox-exec`; on Linux it re-executes itself under Landlock, with a user and network namespace when the kernel allows it.

Let $W$ be the writable set: the project root, the temporary directories, a few devices such as `/dev/null`, and the user's caches. Let $R^{-}$ be the deny-read set: `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.docker`, `~/.netrc`, the macOS keychains, and tenon's own configuration, data and policy directories. For a confined process $q$ and every file $f$:

$$
\mathrm{write}_q(f) \Rightarrow f \in W, \qquad \mathrm{read}_q(f) \Rightarrow f \notin R^{-}, \qquad \mathrm{net}_q = \varnothing \text{ unless granted.}
$$

The third clause deserves precision, because it depends on the kernel. On macOS it is `(deny network*)`. On Linux it is an empty network namespace (loopback only) when user namespaces are available; otherwise, on Landlock ABI $\ge 4$, TCP `bind` and `connect` are denied but **UDP stays open**. tenon reports which level it obtained, and `[sandbox] mode = "required"` turns "could not confine" into "will not run". The `strict-local` profile forces that mode.

Two further details are invariants in their own right. The command's environment is tenon's environment **minus** every variable that holds a provider key, so a confined command cannot read the key that pays for the model. And network access is granted per command, by a global rule on the `shell` tool or by an explicit "allow with network" answer, never remembered for the session.

## 7. The audit chain

The audit log is a sequence of JSON lines in `<data dir>/tenon/audit/YYYY-MM-DD.jsonl`, one per event, each with the fields `seq, ts, session, type, payload, prev, hash` in that fixed order. The payload is redacted before it is written.

!!! definition "Definition 5 (Chain)"
    Let $J(a)$ be the canonical JSON of a line with its `hash` field omitted, and $\Vert$ concatenation. With $h_0 = \texttt{"sha256:0"}$,

    $$
    h_i = \mathrm{SHA256}\big(h_{i-1} \,\Vert\, J(a_i)\big), \qquad \mathtt{prev}(a_i) = h_{i-1}.
    $$

(The previous hash appears twice: as a prefix and inside $J(a_i)$ through `prev`. This is redundant, and harmless.) The chain runs across sessions and across days, in file-name order.

Verification (`tenon audit verify`) recomputes the chain and checks, for each line, that it parses, that `prev` equals the previous hash, that the recomputed hash equals `hash`, and that re-serialising the line gives **exactly the same bytes**. The last check matters: without canonicality, two different byte strings could carry the same parsed content, and the hash would protect the parse rather than the file.

!!! proposition "Proposition 7 (Tamper evidence)"
    Let $a_1..a_n$ verify with head $h_n$. Producing a different sequence $a'_1..a'_m$ that verifies with the same head $h'_m = h_n$, of the same length and differing in some line, requires a collision of SHA-256.

!!! proof "Proof sketch"
    Let $i$ be the last index where the two sequences differ, so $a_k = a'_k$ for $k > i$. Walk back from the head: equal heads and equal lines after $i$ force $h_i = h'_i$. Then $\mathrm{SHA256}(h_{i-1} \Vert J(a_i)) = \mathrm{SHA256}(h'_{i-1} \Vert J(a'_i))$ with $a_i \ne a'_i$. By canonicality $J(a_i) \ne J(a'_i)$, so the two inputs differ: a collision.

How unlikely is that? For an ideal 256-bit hash, the birthday bound gives the probability of *any* collision among $q$ evaluations as

$$
p \;\le\; \frac{q(q-1)}{2^{257}} \;\approx\; \frac{q^2}{2^{257}}.
$$

Even with $q = 2^{64}$ hashes, far more than any log will ever contain, $p \approx 2^{-129}$.

Proposition 7 says nothing about **deletion of the tail**: dropping $a_{k+1}..a_n$ leaves a perfectly valid chain with head $h_k$. A hash chain proves integrity *relative to a known head*. This is the classical limit of forward-secure logs[^sk], and tenon answers it the classical way, with time-stamping in the style of Haber and Stornetta[^hs]: the 32 bytes of the head hash are sent to an RFC 3161 time-stamping authority[^rfc3161], with a random 64-bit nonce, and the signed token is kept. `tenon audit verify` then checks every token against its roots and requires that the anchored digest **is a hash of the chain**. A token anchors the prefix up to its digest at a time $t$; deleting anything before that point becomes detectable, and the verifier reports how many lines follow the last anchor.

The log is also **fail-closed**. A write takes an exclusive lock, re-reads the head from disk, appends, and calls `fsync`; on any failure it truncates back to the previous size and the writer refuses all further writes. The engine writes the audit sink *before* the session sink, and after a failure it refuses to emit anything else.

!!! proposition "Proposition 8 (No effect without a record)"
    Every tool execution is preceded by a `ToolCallRequested` record that was durably written. If the audit log cannot be written, the engine refuses every later event and every new turn, so no further action is executed.

A tool runs only after `runTool` has emitted its request, and an emission error ends the turn before `decideAndRun` is reached. Combined with the fail-closed writer, the log may *end early*, but it never *omits* an action that ran.

Note what is **not** claimed: audit lines are not signed, so anyone who can write to the data directory can rebuild a consistent chain from scratch. The time-stamp tokens, held by a third party's signature, are what turn "consistent" into "consistent with what existed at time $t$".

## 8. Context as a resource: hysteresis and compaction

The model sees a *view* $V(H)$ of the history, rebuilt on every send. tenon estimates the size of a view in tokens as

$$
\tau(V) = \left\lfloor \frac{\#\mathrm{runes}(V)}{4} \right\rfloor,
$$

counting messages, tool-call arguments and tool schemas. Let $w$ be the context window of the model. Two mechanisms keep $\tau$ under control.

**Masking with hysteresis.** First, identical tool results (same tool, same arguments, same SHA-256 of output) are deduplicated: only the latest is kept. Then, if

$$
\tau(V) > 0.5\, w,
$$

the oldest tool results are replaced by a short stub (tool, arguments, line count, a 12-hex-digit hash, and an invitation to run it again) **until**

$$
\tau(V) < 0.4\, w .
$$

Results from the last three tool-using iterations, and sub-agent results, are never masked, and a stub is applied only if it is shorter than what it replaces. Masked results stay masked on later views.

The two thresholds form a hysteresis loop, the same device a thermostat uses. With a single threshold $\theta$, a view oscillating around $\theta$ would mask and unmask at every step, and each change would invalidate the provider's prompt cache. With the gap $[0.4w, 0.5w]$, a masking episode buys at least $0.1\,w$ tokens of growth before the next one.

**Compaction.** If the view still exceeds

$$
\tau(V) > \theta\, w, \qquad \theta = 0.8 \text{ by default, } \theta \in (0,1),
$$

tenon summarises. It keeps the system prompt $H_0$, keeps the longest tail starting at a user message whose size is at most $0.3\,w$, and replaces everything in between by a model-written summary. If the provider still answers "context length exceeded", tenon forces one compaction and retries **exactly once**. If the tail alone is too large, the turn ends with `context_too_large` rather than silently dropping the user's last message.

Why this matters for cost is a short computation. An agent resends the whole view at each of its $n$ iterations. If every iteration adds about $s$ tokens, the input cost without any bound is

$$
\sum_{k=1}^{n} k\,s \;=\; \frac{n(n+1)}{2}\, s \;=\; \Theta(n^2),
$$

whereas with the view bounded by $\theta w$ it is at most

$$
\sum_{k=1}^{n} \min(k\,s,\; \theta w) \;\le\; n\,\theta w \;=\; O(n).
$$

Masking and compaction turn a quadratic bill into a linear one. On top of that, tool definitions are sorted by name so that the prefix of every request is byte-stable, which is what lets providers cache it.

## 9. Termination and rewind

A safety property says nothing bad happens; a liveness property says something good eventually does[^as]. The something good, for an agent, is that a turn **ends**.

!!! proposition "Proposition 9 (Every turn terminates)"
    A turn performs at most $M$ iterations of the loop, with $M = 50$ by default. Each iteration performs a bounded number of provider calls and at most $k$ tool calls, each of which is bounded in time. Hence every turn terminates, with one of the reasons `done`, `max_iterations`, `incomplete`, `cancelled`, `blocked`, `blocked_by_hook`, `budget_exceeded`, `context_too_large` or `error`.

The bounds are concrete. A provider call is retried at most $3$ times, with back-off $1\,\mathrm{s} \cdot 2^{a}$ capped at $60\,\mathrm{s}$; a context-length error triggers at most one forced compaction; a shell command has a 2-minute default timeout and is killed with its whole process group. A `Stop` hook that keeps the agent going consumes an iteration like any other, so hooks cannot extend the bound. A sub-agent runs its own loop with the same $M$, and cannot spawn sub-agents of its own: the `agent` tool is removed from its registry, so the recursion depth is $1$.

The token budget is a weaker, *soft* bound: it is checked when a turn or a sub-agent starts, so a turn may finish slightly above the budget, but no new turn starts once $u \ge B$. A single warning is emitted when $u \ge 0.8\,B$.

**Rewind.** Before each write, tenon copies the previous content of the file to a content-addressed store: a file with digest $x = \mathrm{SHA256}(\text{content})$ lives at `x[0:2]/x`. Each prompt is a restore point $\rho_t$. Restoring is a function

$$
\mathrm{restore}(\rho_t)\colon\; \Phi \mapsto \Phi_t,
$$

and before a byte is written back its digest is checked against its name. Content addressing makes the store deduplicating for free (two identical versions are one object) and makes corruption detectable rather than silent. Rewind is a user command only; the model cannot invoke it.

## 10. The supply chain as a function

All of the above is a property of a program. The last question is whether the binary you run **is** that program.

A tenon release is built with vendored dependencies (no module proxy), `CGO_ENABLED=0`, `-trimpath`, an empty build ID, and `SOURCE_DATE_EPOCH` set to the time of the last commit. Archives are written with sorted names, fixed owners and modes, fixed modification times and `gzip -n`. The intent is that the build is a **function**:

$$
\mathrm{build}\colon (\text{source}, \text{toolchain}, \text{target}) \longmapsto \text{bytes},
$$

with no hidden argument such as the clock, the path of the checkout or the order of a directory listing. `scripts/repro-check.sh` tests this: it builds every target twice, in separate directories with separate Go caches, and fails on any difference in SHA-256.

Distribution is then a chain of two checks. `SHA256SUMS` lists the digest of every artefact and is signed with minisign, an Ed25519 signature whose public key is `tenon.pub` at the root of the repository. The install script always verifies the archive against `SHA256SUMS`, and verifies the signature of `SHA256SUMS` when `minisign` is available (always, with `TENON_REQUIRE_SIGNATURE=1`, which is what the CI templates set). A CycloneDX SBOM ships with every release.

In CI, tenon runs without an interface and the approval function is the constant $\mathrm{approve} \equiv \mathsf{false}$: every question is answered no, and the run exits with code $3$. With `--strict`, a turn that did not end in `done` exits $4$. The exit code is the maximum-priority failure, in the order $1 > 3 > 4 > 0$, so a pipeline can tell "broken" from "refused" from "unfinished" without parsing logs.

## 11. What is not proved

An honest list matters more than any proposition above.

- **The kernel.** The sandbox is as good as Landlock and Seatbelt. On Linux without user namespaces and with an older Landlock ABI, UDP is not confined.
- **Jurisdiction is declared, not measured.** $j(p)$ comes from configuration. tenon enforces the consequences of a declaration; it does not geolocate a server.
- **Redaction is pattern recognition.** A secret of fewer than 8 characters, or in a format no rule knows, is not masked. Streamed fragments are redacted one by one, so a secret split across two fragments can pass.
- **The human.** Proposition 1 guarantees that a question is asked. It does not guarantee that the answer was read.
- **The tail of the log.** Between the last time-stamp and now, deleting lines is undetectable without an external copy of the head.
- **These are paper proofs.** The propositions describe a model of the code, checked by reading it, not by a proof assistant. A bug in the code is a counter-example to the model, and should be reported as such.

## Conclusion

The thesis of this essay fits in one line: **the model proposes, the harness disposes**. Nothing in §§2–10 depends on the model being good. The decision order, the policy floor, the rising label, the one-way markers, the confined process, the hash chain, the hysteresis band, the bounded loop and the reproducible build are all properties of a deterministic program that can be read, tested and audited. That is the only kind of guarantee one can honestly offer about a system whose most capable component is, by construction, a black box.

## References

1. J. H. Saltzer and M. D. Schroeder. *The Protection of Information in Computer Systems.* Proceedings of the IEEE, 63(9), 1975.
2. L. Lamport. *Proving the Correctness of Multiprocess Programs.* IEEE Transactions on Software Engineering, SE-3(2), 1977.
3. B. Alpern and F. B. Schneider. *Defining Liveness.* Information Processing Letters, 21(4), 1985.
4. D. E. Denning. *A Lattice Model of Secure Information Flow.* Communications of the ACM, 19(5), 1976.
5. S. Haber and W. S. Stornetta. *How to Time-Stamp a Digital Document.* Journal of Cryptology, 3(2), 1991.
6. B. Schneier and J. Kelsey. *Secure Audit Logs to Support Computer Forensics.* ACM Transactions on Information and System Security, 2(2), 1999.
7. C. Adams, P. Cain, D. Pinkas and R. Zuccherato. *Internet X.509 Public Key Infrastructure Time-Stamp Protocol (TSP).* RFC 3161, 2001.
8. D. J. Bernstein, N. Duif, T. Lange, P. Schwabe and B.-Y. Yang. *High-Speed High-Security Signatures.* Journal of Cryptographic Engineering, 2(2), 2012.
9. ISO 7064:2003. *Information technology — Security techniques — Check character systems.*
10. Reproducible Builds project. *Definitions and SOURCE_DATE_EPOCH specification.* reproducible-builds.org.

The code referred to throughout is in the [tenon repository](https://github.com/tenon-code-eu/tenon), mainly `internal/engine`, `internal/policy`, `internal/audit`, `internal/routing`, `internal/sandbox` and `scripts/`. The limits in §11 are also listed in [Security and limits](../../security.md).

## Notes

[^ss]: Saltzer and Schroeder list eight principles; a harness relies most on *fail-safe defaults*, *complete mediation* and *least privilege*.
[^lamport]: Lamport introduced the safety/liveness vocabulary for concurrent programs; Alpern and Schneider later proved that every property is the intersection of a safety and a liveness property.
[^ed25519]: minisign signs either the message (legacy `Ed`) or its BLAKE2b-512 digest (`ED`); tenon accepts both and also checks the global signature over the trusted comment.
[^denning]: Denning's model is a lattice of classes with a flow relation; tenon's two-point label with a high-water mark is its simplest non-trivial instance.
[^sk]: Schneier and Kelsey's construction adds evolving keys so that an attacker who compromises the machine later cannot rewrite earlier entries; tenon chooses external time-stamps instead of keys held on the machine.
[^hs]: Haber and Stornetta's insight is that linking documents by hashes and publishing a digest of the chain makes back-dating infeasible.
[^rfc3161]: The request carries the SHA-256 OID, a nonce that the response must echo, and asks for the authority's certificate. Revocation (CRL, OCSP) is not checked.
[^as]: Alpern and Schneider's definition: a liveness property is one where every finite prefix can still be extended to an execution that satisfies it.
