import type { RequestPermissionRequest } from "@agentclientprotocol/sdk";
import { describe, expect, it, vi } from "vitest";
import { Conversation, type Item } from "../../src/model";
import { Permissions } from "../../src/permissions";

/** A deferred promise. */
function later<T>(): { promise: Promise<T>; resolve: (v: T) => void } {
  let resolve: (v: T) => void = () => undefined;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

const OPTIONS = [
  { optionId: "allow", name: "Allow", kind: "allow_once" },
  { optionId: "allow_network", name: "Allow with network", kind: "allow_once" },
  { optionId: "allow_session", name: "Always for this session", kind: "allow_always" },
  { optionId: "reject", name: "Reject", kind: "reject_once" },
] as const;

function req(title = "write_file · a.txt", toolCallId = "call_1"): RequestPermissionRequest {
  return { sessionId: "s1", toolCall: { toolCallId, title }, options: OPTIONS.map((o) => ({ ...o })) };
}

type Card = Extract<Item, { kind: "permission" }>;

function cards(conv: Conversation): Card[] {
  return conv.state.items.filter((i): i is Card => i.kind === "permission");
}

type Notify = (message: string, ...names: string[]) => Promise<string | undefined>;

function setup(visible = true, notify = vi.fn<Notify>(() => Promise.resolve(undefined))) {
  const conv = new Conversation();
  conv.begin("s1", "/p");
  const log = { info: vi.fn(), stderr: vi.fn() };
  const perms = new Permissions(conv, { visible: () => visible, notify, log });
  return { conv, perms, notify };
}

describe("Permissions", () => {
  it("adds a card with tenon's options, in tenon's order and with tenon's names", () => {
    const { conv, perms } = setup();
    void perms.request(req());
    expect(cards(conv)).toEqual([
      {
        kind: "permission",
        requestId: "p1",
        toolCallId: "call_1",
        title: "write_file · a.txt",
        options: OPTIONS.map((o) => ({ ...o })),
      },
    ]);
  });

  it("answers a click with the selected option and marks the card answered", async () => {
    const { conv, perms } = setup();
    const p = perms.request(req());
    perms.answer("p1", "allow");
    expect(await p).toEqual({ outcome: { outcome: "selected", optionId: "allow" } });
    expect(cards(conv)[0].answered).toBe("allow");
    expect(perms.pendingIds()).toEqual([]);
  });

  it("ignores an option tenon did not offer and an unknown request", async () => {
    const { perms } = setup();
    const p = perms.request(req());
    perms.answer("p1", "always_everything");
    perms.answer("p9", "allow");
    expect(perms.pendingIds()).toEqual(["p1"]);
    perms.answer("p1", "reject");
    expect(await p).toEqual({ outcome: { outcome: "selected", optionId: "reject" } });
  });

  it("shows no notification while the chat view is visible", () => {
    const { perms, notify } = setup(true);
    void perms.request(req());
    expect(notify).not.toHaveBeenCalled();
  });

  it("notifies with the option names when the view is hidden; a choice there answers", async () => {
    const notify = vi.fn<Notify>(() => Promise.resolve("Always for this session"));
    const { conv, perms } = setup(false, notify);
    const p = perms.request(req());
    expect(notify).toHaveBeenCalledTimes(1);
    const [message, ...names] = notify.mock.calls[0];
    expect(message).toContain("write_file · a.txt");
    expect(names).toEqual(["Allow", "Allow with network", "Always for this session", "Reject"]);
    expect(await p).toEqual({ outcome: { outcome: "selected", optionId: "allow_session" } });
    expect(cards(conv)[0].answered).toBe("allow_session");
  });

  it("a dismissed notification answers cancelled and the card shows it refused", async () => {
    const { conv, perms } = setup(false);
    const p = perms.request(req());
    expect(await p).toEqual({ outcome: { outcome: "cancelled" } });
    expect(cards(conv)[0].refused).toBe(true);
    expect(cards(conv)[0].answered).toBeUndefined();
  });

  it("the first answer wins: a late notification answer is ignored", async () => {
    const shown = later<string | undefined>();
    const { perms } = setup(
      false,
      vi.fn<Notify>(() => shown.promise),
    );
    const p = perms.request(req());
    perms.answer("p1", "reject");
    shown.resolve("Allow");
    await shown.promise;
    expect(await p).toEqual({ outcome: { outcome: "selected", optionId: "reject" } });
  });

  it("cancelAll answers every pending request cancelled", async () => {
    const { conv, perms } = setup();
    const a = perms.request(req("one", "c1"));
    const b = perms.request(req("two", "c2"));
    perms.cancelAll();
    expect(await a).toEqual({ outcome: { outcome: "cancelled" } });
    expect(await b).toEqual({ outcome: { outcome: "cancelled" } });
    expect(cards(conv).map((c) => c.refused)).toEqual([true, true]);
    // A click on a card that is already closed changes nothing.
    perms.answer("p1", "allow");
    expect(cards(conv)[0].answered).toBeUndefined();
  });

  it("two requests, the second answered first: each gets its own option", async () => {
    const { conv, perms } = setup();
    const a = perms.request(req("one", "c1"));
    const b = perms.request(req("two", "c2"));
    expect(cards(conv).map((c) => c.requestId)).toEqual(["p1", "p2"]);
    expect(perms.pendingIds()).toEqual(["p1", "p2"]);
    perms.answer("p2", "reject");
    perms.answer("p1", "allow");
    expect(await a).toEqual({ outcome: { outcome: "selected", optionId: "allow" } });
    expect(await b).toEqual({ outcome: { outcome: "selected", optionId: "reject" } });
    expect(cards(conv).map((c) => c.answered)).toEqual(["allow", "reject"]);
  });

  it("a request without a tool call id still gets a card", () => {
    const { conv, perms } = setup();
    void perms.request({ sessionId: "s1", toolCall: { toolCallId: "", title: "provider" }, options: [] });
    expect(cards(conv)[0].toolCallId).toBeUndefined();
  });
});
