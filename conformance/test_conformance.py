"""Conformance suite: proves the pinned engine + Pareto config behave.

Run via conformance/run.sh. These are LIVE tests — they spend real (small)
Pareto turns through the production gateway, which is the point: the suite
exists to catch engine-version and gateway drift before a bump lands.

Covers the milestones proven during the 2026-08-12 spike:
  1. one question -> streamed answer (basic_stream)
  2. multi-turn context + mid-stream interrupt (agentic loop, part 1)
  3. command-approval round-trip (agentic loop, part 2)
"""
import queue
import sys
import time

from client import Client

FAILURES = []


def check(name, ok, detail=""):
    print(f"  {'PASS' if ok else 'FAIL'}  {name}" + (f"  ({detail})" if detail else ""))
    if not ok:
        FAILURES.append(name)


def run_turn(c, tid, text, timeout=90):
    parts = []

    def note(m):
        if m["method"] == "item/agentMessage/delta":
            parts.append(m["params"].get("delta", ""))

    c.wait_response(
        c.request("turn/start", {"threadId": tid, "input": [{"type": "text", "text": text}]}),
        on_notification=note,
    )
    done = c.pump("turn/completed", timeout=timeout, on_notification=note)
    return "".join(parts), done["turn"].get("status")


def test_basic_stream(c):
    print("basic_stream:")
    tid = c.wait_response(
        c.request("thread/start", {"ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
    )["thread"]["id"]
    answer, status = run_turn(c, tid, "In one short sentence, what is a load balancer?")
    check("turn completes", status == "completed", f"status={status}")
    check("answer streamed", len(answer) > 20, f"{len(answer)} chars")


def test_multiturn_and_interrupt(c):
    print("multiturn_and_interrupt:")
    tid = c.wait_response(
        c.request("thread/start", {"ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
    )["thread"]["id"]

    _, s1 = run_turn(c, tid, "My favorite number is 47. Acknowledge in five words or fewer.")
    r2, _ = run_turn(c, tid, "What is my favorite number? Answer with just the number.")
    check("context carries across turns", "47" in r2, f"reply={r2.strip()[:40]!r}")

    # Interrupt: start a long generation, cut it off after the first deltas.
    chars = []
    turn_box = {}

    def note(m):
        if m["method"] == "turn/started":
            turn_box["id"] = m["params"]["turn"]["id"]
        elif m["method"] == "item/agentMessage/delta":
            chars.append(m["params"].get("delta", ""))

    start = c.wait_response(
        c.request(
            "turn/start",
            {"threadId": tid, "input": [{"type": "text", "text": "Write a poem about the ocean, at least 60 lines long."}]},
        ),
        on_notification=note,
    )
    turn_id = start.get("turn", start).get("id") or turn_box.get("id")

    t0 = time.time()
    while sum(len(x) for x in chars) < 120 and time.time() - t0 < 60:
        try:
            msg = c.next_msg(timeout=1)
        except queue.Empty:
            continue
        if msg and "method" in msg and "id" not in msg:
            note(msg)

    c.wait_response(c.request("turn/interrupt", {"threadId": tid, "turnId": turn_id}), on_notification=note)
    done = c.pump("turn/completed", timeout=30, on_notification=note)
    check("interrupt ends turn as interrupted", done["turn"].get("status") == "interrupted",
          f"status={done['turn'].get('status')}, streamed={sum(len(x) for x in chars)}")


def test_command_approval(c, cwd):
    print("command_approval:")
    tid = c.wait_response(
        c.request("thread/start", {"ephemeral": True, "approvalPolicy": "untrusted", "sandbox": "read-only", "cwd": cwd})
    )["thread"]["id"]
    approvals = []
    parts = []
    commands = []

    def on_req(m):
        if m["method"] == "item/commandExecution/requestApproval":
            approvals.append(m["params"].get("command"))
            c.send({"id": m["id"], "result": {"decision": "accept"}})
        else:
            # An unexpected server request means the engine grew a new ask;
            # decline it and let the assertion below flag the drift.
            approvals.append(f"UNEXPECTED:{m['method']}")
            c.send({"id": m["id"], "result": {"decision": "decline"}})

    def note(m):
        if m["method"] == "item/agentMessage/delta":
            parts.append(m["params"].get("delta", ""))
        elif m["method"] == "item/completed" and m["params"].get("item", {}).get("type") == "commandExecution":
            commands.append(m["params"]["item"].get("status"))

    # The command must NOT be on codex's trusted list, or `untrusted` policy
    # runs it without asking (uname, ls, cat etc. are pre-trusted — the first
    # run of this suite proved that with uname). sw_vers / lscpu are obscure
    # enough to require approval on their respective platforms.
    probe = "sw_vers" if sys.platform == "darwin" else "lscpu"
    c.wait_response(
        c.request(
            "turn/start",
            {"threadId": tid, "input": [{"type": "text", "text": f"Run the shell command `{probe}` and tell me its output, nothing else."}]},
        ),
        on_notification=note,
        on_server_request=on_req,
    )
    done = c.pump("turn/completed", timeout=120, on_notification=note, on_server_request=on_req)
    answer = "".join(parts).strip()

    check("approval was requested", any(a and not str(a).startswith("UNEXPECTED:") for a in approvals),
          f"approvals={approvals}")
    check("approved command completed", "completed" in commands, f"commands={commands}")
    check("turn completes with answer", done["turn"].get("status") == "completed" and len(answer) > 0,
          f"answer={answer[:40]!r}")


def main():
    import os
    import tempfile

    c = Client()
    try:
        init = c.handshake()
        print(f"engine initialized (codexHome={init.get('codexHome', '?')})")
        test_basic_stream(c)
        test_multiturn_and_interrupt(c)
        with tempfile.TemporaryDirectory() as cwd:
            test_command_approval(c, cwd)
    finally:
        c.close()

    if FAILURES:
        print(f"\n{len(FAILURES)} failure(s): {', '.join(FAILURES)}")
        sys.exit(1)
    print("\nall conformance checks passed")


if __name__ == "__main__":
    main()
