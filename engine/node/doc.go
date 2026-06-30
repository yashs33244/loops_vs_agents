// Package node defines the Executor abstraction: how a single node's work is
// actually run, given its already-satisfied dependency outputs. The scheduler
// owns the graph; an Executor owns one node's execution.
//
// Three implementations cover the spec's scenarios:
//
//   - MockExecutor: deterministic, scriptable (including injected failures) for
//     tests.
//   - LLMExecutor: builds a prompt from the node plus its inputs, calls a
//     provider.Provider, and checks the result against the node's contract.
//   - ToolExecutor: jailed file/command operations, gated by the node's
//     side-effect level (decision D4).
package node
