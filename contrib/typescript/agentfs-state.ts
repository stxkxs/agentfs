/**
 * Reference writer for the agentfs agent-state contract, version agentfs/v1.
 *
 * An agent declares what it is doing by writing one JSON document into its
 * workspace directory. This module builds that document and publishes it
 * atomically, so a reader that opens the file at any moment sees a whole
 * document rather than a partial one.
 *
 * Vendor this file. It is one module and imports only Node's standard library.
 *
 *     import { Status, writeState } from "./agentfs-state.ts";
 *
 *     writeState("/srv/agents/indexer", { status: Status.Running, task: "Index the tree" });
 *
 * `agentfs schema` prints the JSON Schema this writer emits against, and
 * `agentfs validate <workspace>` reports what a reader makes of a document.
 *
 * The writer refuses what the decoder refuses: an unknown status, a negative
 * ordinal, an empty string where a value is expected, a timestamp that names no
 * instant. A document it writes carries no error-severity diagnostic.
 *
 * Every construct here erases to JavaScript, so Node runs the file as it
 * stands: `node agentfs-state.ts <workspace>`.
 */

import { randomUUID } from "node:crypto";
import { closeSync, fsyncSync, openSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

/** The contract version this writer emits. */
export const SCHEMA_VERSION = "agentfs/v1";

/** The document name a reader looks for in a workspace directory. */
export const STATE_FILE = "state.json";

/** The ceiling on a name-shaped member: agent, model, runId. */
export const MAX_NAME_CHARS = 256;

/** The ceiling on a prose member: task, problem. */
export const MAX_TEXT_CHARS = 4096;

/**
 * The state an agent declares itself to be in.
 *
 * The vocabulary is closed. A reader refuses a value outside it rather than
 * guessing, so a status that merely contains one of these words, such as
 * "not running", declares nothing.
 */
export const Status = {
  /** The agent holds work and is progressing. */
  Running: "running",
  /** The agent holds no work and is available. */
  Idle: "idle",
  /** The agent holds work it cannot progress without an external input. */
  Blocked: "blocked",
  /** The agent stopped because it could not continue. */
  Error: "error",
  /** The agent completed its work. */
  Done: "done",
} as const;

/** One of the statuses the contract defines. */
export type Status = (typeof Status)[keyof typeof Status];

/** Every status the contract defines, in contract order. */
export const STATUSES: readonly Status[] = Object.values(Status);

/**
 * One agent state document.
 *
 * Members read in camel case and are written under the contract's names.
 *
 * `status` is authoritative and independent of `problem`: an agent that
 * finished after a recovered fault declares `Done` and a problem describing it,
 * and one progressing after a transient failure declares `Running` and a
 * problem. Deriving one from the other makes both unrepresentable.
 */
export interface State {
  /** The state the agent declares itself to be in. */
  status: Status;
  /** The agent's name. A reader uses the workspace directory name when absent. */
  agent?: string;
  /** The work in progress. */
  task?: string;
  /** The position within the task: a non-negative ordinal or a named phase. */
  step?: number | string;
  /** The number of steps the task is expected to take. */
  stepsTotal?: number;
  /** The model the agent is running against. */
  model?: string;
  /** The run directory this document belongs to. */
  runId?: string;
  /** A fault description, independent of status. */
  problem?: string;
  /**
   * How often the agent undertakes to rewrite the document. A document older
   * than this reads as stale.
   */
  heartbeatSeconds?: number;
  /** When the agent began the task. */
  startedAt?: Date;
  /** When the agent wrote the document. */
  updatedAt?: Date;
  /** Integrator-defined string metadata. */
  labels?: Record<string, string>;
  /** Members the contract does not define. A reader preserves and ignores them. */
  extra?: Record<string, unknown>;
}

/**
 * Build the document as a plain object, in contract order.
 *
 * Throws when a member holds a value a reader would refuse.
 */
export function stateDocument(state: State): Record<string, unknown> {
  if (!STATUSES.includes(state.status)) {
    throw new Error(`status ${JSON.stringify(state.status)} is outside the vocabulary: ${STATUSES.join(", ")}`);
  }

  const out: Record<string, unknown> = { schema: SCHEMA_VERSION, status: state.status };
  put(out, "agent", text(state.agent, "agent", MAX_NAME_CHARS));
  put(out, "task", text(state.task, "task", MAX_TEXT_CHARS));
  put(out, "step", step(state.step));
  put(out, "steps_total", count(state.stepsTotal, "stepsTotal"));
  put(out, "model", text(state.model, "model", MAX_NAME_CHARS));
  put(out, "run_id", text(state.runId, "runId", MAX_NAME_CHARS));
  put(out, "problem", text(state.problem, "problem", MAX_TEXT_CHARS));
  put(out, "heartbeat_seconds", count(state.heartbeatSeconds, "heartbeatSeconds"));
  put(out, "started_at", state.startedAt === undefined ? undefined : rfc3339(state.startedAt));
  put(out, "updated_at", state.updatedAt === undefined ? undefined : rfc3339(state.updatedAt));
  put(out, "labels", labels(state.labels));

  for (const [name, value] of Object.entries(state.extra ?? {})) {
    if (name in out) {
      throw new Error(
        `extra member ${JSON.stringify(name)} collides with a contract member; ` +
          "rename it or set the contract member itself",
      );
    }
    out[name] = value;
  }
  return out;
}

/** Encode the document as the text to publish. */
export function encodeState(state: State): string {
  return `${JSON.stringify(stateDocument(state), null, 2)}\n`;
}

/** Publish the document into `directory` and return the path written. */
export function writeState(directory: string, state: State, name: string = STATE_FILE): string {
  const target = join(directory, name);
  writeAtomic(target, encodeState(state));
  return target;
}

/**
 * Render `moment` as an RFC 3339 date-time with its offset.
 *
 * A date-time without an offset names no instant, and a reader on another host
 * cannot place it, so the rendering always carries one.
 */
export function rfc3339(moment: Date): string {
  if (Number.isNaN(moment.getTime())) {
    throw new Error("a timestamp names an instant: this Date holds none");
  }
  return moment.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/**
 * Publish `data` at `target` in one step.
 *
 * A writer that opens the target and streams into it publishes every
 * intermediate state, and a reader that opens the file meanwhile sees bytes
 * that are not JSON. Writing the whole document to a temporary file in the same
 * directory and renaming it over the target publishes the change atomically: a
 * reader observes either the previous document or this one.
 *
 * The temporary file shares the target's directory because rename is atomic
 * only within one filesystem. The fsync before the rename orders the bytes
 * ahead of the name that points at them, so a document that survives a crash is
 * a whole document; the fsync on the directory afterwards is what makes the name
 * itself survive.
 */
export function writeAtomic(target: string, data: string): void {
  const directory = dirname(resolve(target));
  const temporary = join(directory, `.agentfs-state-${randomUUID()}.json`);

  const handle = openSync(temporary, "wx", 0o644);
  try {
    writeFileSync(handle, data, { encoding: "utf8" });
    fsyncSync(handle);
  } finally {
    closeSync(handle);
  }

  try {
    renameSync(temporary, target);
  } catch (cause) {
    discard(temporary);
    throw cause;
  }
  fsyncDirectory(directory);
}

/**
 * Flush the directory entry the rename created.
 *
 * Platforms that cannot open a directory as a file report the attempt rather
 * than the flush, and the rename is already visible to every reader.
 */
function fsyncDirectory(directory: string): void {
  let handle: number;
  try {
    handle = openSync(directory, "r");
  } catch {
    return;
  }
  try {
    fsyncSync(handle);
  } catch {
    // The name is visible; only its durability across a crash is at stake.
  } finally {
    closeSync(handle);
  }
}

/** Remove a temporary file whose rename did not happen. */
function discard(path: string): void {
  try {
    unlinkSync(path);
  } catch {
    // The temporary file is already gone.
  }
}

function put(out: Record<string, unknown>, name: string, value: unknown): void {
  if (value !== undefined) {
    out[name] = value;
  }
}

function text(value: string | undefined, name: string, ceiling: number): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new Error(`${name} holds a string, not ${typeof value}`);
  }
  if (value === "") {
    throw new Error(`${name} is empty; omit it rather than declaring it empty`);
  }
  // A reader counts code points, so the ceiling is measured in them rather
  // than in the UTF-16 units String.length reports: an emoji is one character
  // to the contract and two to length.
  const characters = [...value].length;
  if (characters > ceiling) {
    throw new Error(
      `${name} is ${characters} characters, past the ${ceiling} the contract allows; ` +
        "a state document is a status declaration, not a log",
    );
  }
  return value;
}

function step(value: number | string | undefined): number | string | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value === "string") {
    return text(value, "step", MAX_NAME_CHARS);
  }
  if (!Number.isInteger(value)) {
    throw new Error("step holds a whole ordinal or a phase name");
  }
  if (value < 0) {
    throw new Error("step is negative; an ordinal counts from zero");
  }
  return value;
}

function count(value: number | undefined, name: string): number | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`${name} holds a finite number`);
  }
  if (value < 0) {
    throw new Error(`${name} is negative`);
  }
  return value;
}

function labels(value: Record<string, string> | undefined): Record<string, string> | undefined {
  if (value === undefined || Object.keys(value).length === 0) {
    return undefined;
  }
  for (const [key, held] of Object.entries(value)) {
    if (typeof held !== "string") {
      throw new Error(`label ${JSON.stringify(key)} holds a string value, not ${typeof held}`);
    }
  }
  return { ...value };
}

/**
 * Write one document, declaring every member the contract defines.
 *
 * The example carries a `problem` while running, because `status` is
 * authoritative and independent of it: an agent progressing after a transient
 * fault declares both.
 */
export function example(directory: string): string {
  const started = new Date();
  return writeState(directory, {
    status: Status.Running,
    agent: "indexer",
    task: "Index the workspace tree",
    step: 3,
    stepsTotal: 8,
    model: "claude-opus-5",
    runId: "2026-04-08T12-40-11Z-1f3c",
    problem: "The first two fetches timed out and were retried.",
    heartbeatSeconds: 15,
    startedAt: started,
    updatedAt: started,
    labels: { host: "runner-3", queue: "batch" },
  });
}

// Node puts the entry point in process.argv[1], so comparing it against this
// file's name runs the example only when the file is invoked directly. The
// comparison is on the name rather than on import.meta, which parses under one
// module system and not the other.
const entry = process.argv[1] === undefined ? "" : resolve(process.argv[1]);
if (entry.endsWith("agentfs-state.ts") || entry.endsWith("agentfs-state.js")) {
  console.log(example(process.argv[2] ?? "."));
}
