"""Reference writer for the agentfs agent-state contract, version agentfs/v1.

An agent declares what it is doing by writing one JSON document into its
workspace directory. This module builds that document and publishes it
atomically, so a reader that opens the file at any moment sees a whole document
rather than a partial one.

Vendor this file. It is one module and imports only the standard library.

    from agentfs_state import State, Status

    State(status=Status.RUNNING, task="Index the tree").write("/srv/agents/indexer")

`agentfs schema` prints the JSON Schema this writer emits against, and
`agentfs validate <workspace>` reports what a reader makes of a document.

The writer refuses what the decoder refuses: an unknown status, a negative
ordinal, an empty string where a value is expected, a timestamp with no offset.
A document it writes carries no error-severity diagnostic.
"""

from __future__ import annotations

import enum
import json
import os
import tempfile
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Dict, Mapping, Optional, Union

__all__ = ["SCHEMA_VERSION", "STATE_FILE", "Status", "State", "rfc3339", "write_atomic"]

#: The contract version this writer emits.
SCHEMA_VERSION = "agentfs/v1"

#: The document name a reader looks for in a workspace directory.
STATE_FILE = "state.json"

#: The ceiling on a name-shaped member: agent, model, run_id.
MAX_NAME_CHARS = 256

#: The ceiling on a prose member: task, problem.
MAX_TEXT_CHARS = 4096


class Status(str, enum.Enum):
    """The state an agent declares itself to be in.

    The vocabulary is closed. A reader refuses a value outside it rather than
    guessing, so a status that merely contains one of these words, such as
    "not running", declares nothing.
    """

    #: The agent holds work and is progressing.
    RUNNING = "running"
    #: The agent holds no work and is available.
    IDLE = "idle"
    #: The agent holds work it cannot progress without an external input.
    BLOCKED = "blocked"
    #: The agent stopped because it could not continue.
    ERROR = "error"
    #: The agent completed its work.
    DONE = "done"

    def __str__(self) -> str:
        return self.value


@dataclass
class State:
    """One agent state document.

    ``status`` is authoritative and independent of ``problem``: an agent that
    finished after a recovered fault declares ``DONE`` and a problem describing
    it, and one progressing after a transient failure declares ``RUNNING`` and a
    problem. Deriving one from the other makes both unrepresentable.
    """

    #: The state the agent declares itself to be in. Required.
    status: Status
    #: The agent's name. A reader uses the workspace directory name when absent.
    agent: Optional[str] = None
    #: The work in progress.
    task: Optional[str] = None
    #: The position within the task: a non-negative ordinal or a named phase.
    step: Optional[Union[int, str]] = None
    #: The number of steps the task is expected to take.
    steps_total: Optional[int] = None
    #: The model the agent is running against.
    model: Optional[str] = None
    #: The run directory this document belongs to.
    run_id: Optional[str] = None
    #: A fault description, independent of status.
    problem: Optional[str] = None
    #: How often the agent undertakes to rewrite the document. A document older
    #: than this reads as stale.
    heartbeat_seconds: Optional[float] = None
    #: When the agent began the task. Requires a time zone.
    started_at: Optional[datetime] = None
    #: When the agent wrote the document. Requires a time zone.
    updated_at: Optional[datetime] = None
    #: Integrator-defined string metadata.
    labels: Dict[str, str] = field(default_factory=dict)
    #: Members the contract does not define. A reader preserves and ignores
    #: them.
    extra: Dict[str, Any] = field(default_factory=dict)

    def document(self) -> Dict[str, Any]:
        """Return the document as a mapping, in contract order.

        Raises ValueError for a value a reader would refuse.
        """
        status = Status(self.status)
        out: Dict[str, Any] = {"schema": SCHEMA_VERSION, "status": status.value}

        _put(out, "agent", _text(self.agent, "agent", MAX_NAME_CHARS))
        _put(out, "task", _text(self.task, "task", MAX_TEXT_CHARS))
        _put(out, "step", _step(self.step))
        _put(out, "steps_total", _count(self.steps_total, "steps_total"))
        _put(out, "model", _text(self.model, "model", MAX_NAME_CHARS))
        _put(out, "run_id", _text(self.run_id, "run_id", MAX_NAME_CHARS))
        _put(out, "problem", _text(self.problem, "problem", MAX_TEXT_CHARS))
        _put(out, "heartbeat_seconds", _count(self.heartbeat_seconds, "heartbeat_seconds"))
        _put(out, "started_at", rfc3339(self.started_at) if self.started_at else None)
        _put(out, "updated_at", rfc3339(self.updated_at) if self.updated_at else None)
        _put(out, "labels", _labels(self.labels))

        for name, value in self.extra.items():
            if name in out:
                raise ValueError(
                    f"extra member {name!r} collides with a contract member; "
                    "rename it or set the contract member itself"
                )
            out[name] = value
        return out

    def encode(self) -> bytes:
        """Return the document as the UTF-8 bytes to publish."""
        text = json.dumps(self.document(), indent=2, ensure_ascii=False)
        return (text + "\n").encode("utf-8")

    def write(self, directory: str, name: str = STATE_FILE) -> str:
        """Publish the document into ``directory`` and return the path written."""
        target = os.path.join(directory, name)
        write_atomic(target, self.encode())
        return target


def rfc3339(moment: datetime) -> str:
    """Render ``moment`` as an RFC 3339 date-time with its offset.

    A naive datetime is refused: without an offset the text names no instant,
    and a reader on another host cannot place it.
    """
    if moment.tzinfo is None or moment.utcoffset() is None:
        raise ValueError(
            "a timestamp needs a time zone: use datetime.now(timezone.utc), "
            "or attach one with astimezone()"
        )
    text = moment.isoformat(timespec="seconds")
    return text[:-6] + "Z" if text.endswith("+00:00") else text


def write_atomic(path: str, data: bytes) -> None:
    """Publish ``data`` at ``path`` in one step.

    A writer that opens the target and streams into it publishes every
    intermediate state, and a reader that opens the file meanwhile sees bytes
    that are not JSON. Writing the whole document to a temporary file in the
    same directory and renaming it over the target publishes the change
    atomically: a reader observes either the previous document or this one.

    The temporary file shares the target's directory because rename is atomic
    only within one filesystem. The fsync before the rename orders the bytes
    ahead of the name that points at them, so a document that survives a crash
    is a whole document; the fsync on the directory afterwards is what makes
    the name itself survive.
    """
    directory = os.path.dirname(os.path.abspath(path))
    handle, temporary = tempfile.mkstemp(prefix=".agentfs-state-", suffix=".json", dir=directory)
    try:
        with os.fdopen(handle, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise
    _fsync_directory(directory)


def _fsync_directory(directory: str) -> None:
    """Flush the directory entry the rename created.

    Platforms that cannot open a directory as a file report the attempt rather
    than the flush, and the rename is already visible to every reader.
    """
    try:
        fd = os.open(directory, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)


def _put(out: Dict[str, Any], name: str, value: Any) -> None:
    if value is not None:
        out[name] = value


def _text(value: Optional[str], name: str, ceiling: int) -> Optional[str]:
    if value is None:
        return None
    if not isinstance(value, str):
        raise ValueError(f"{name} holds a string, not {type(value).__name__}")
    if value == "":
        raise ValueError(f"{name} is empty; omit it rather than declaring it empty")
    if len(value) > ceiling:
        raise ValueError(
            f"{name} is {len(value)} characters, past the {ceiling} the contract allows; "
            "a state document is a status declaration, not a log"
        )
    return value


def _step(value: Optional[Union[int, str]]) -> Optional[Union[int, str]]:
    if value is None:
        return None
    if isinstance(value, bool):
        raise ValueError("step holds an ordinal or a phase name, not a boolean")
    if isinstance(value, int):
        if value < 0:
            raise ValueError("step is negative; an ordinal counts from zero")
        return value
    if isinstance(value, str):
        return _text(value, "step", MAX_NAME_CHARS)
    raise ValueError(f"step holds an ordinal or a phase name, not {type(value).__name__}")


def _count(value: Optional[float], name: str) -> Optional[float]:
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{name} holds a number, not {type(value).__name__}")
    if value < 0:
        raise ValueError(f"{name} is negative")
    return value


def _labels(labels: Mapping[str, str]) -> Optional[Dict[str, str]]:
    if not labels:
        return None
    out: Dict[str, str] = {}
    for key, value in labels.items():
        if not isinstance(key, str) or not isinstance(value, str):
            raise ValueError(f"label {key!r} holds a string key and a string value")
        out[key] = value
    return out


def _example(directory: str) -> str:
    """Write one document, declaring every member the contract defines.

    The example carries a ``problem`` while running, because ``status`` is
    authoritative and independent of it: an agent progressing after a transient
    fault declares both.
    """
    from datetime import timezone

    started = datetime.now(timezone.utc)
    state = State(
        status=Status.RUNNING,
        agent="indexer",
        task="Index the workspace tree",
        step=3,
        steps_total=8,
        model="claude-opus-5",
        run_id="2026-04-08T12-40-11Z-1f3c",
        problem="The first two fetches timed out and were retried.",
        heartbeat_seconds=15,
        started_at=started,
        updated_at=started,
        labels={"host": "runner-3", "queue": "batch"},
    )
    return state.write(directory)


if __name__ == "__main__":
    import sys

    print(_example(sys.argv[1] if len(sys.argv) > 1 else "."))
