// session/request_permission: one card in the thread per request, plus a
// notification when the chat view is hidden. The first answer (card or
// notification) goes to tenon; a dismissed notification, Stop or tenon's exit
// answer "cancelled", which tenon takes as a refusal. Nothing is ever
// answered automatically and nothing is remembered here.
import type { RequestPermissionRequest, RequestPermissionResponse } from "@agentclientprotocol/sdk";
import * as vscode from "vscode";
import type { Log } from "./log";
import type { Conversation, Item } from "./model";

type Card = Extract<Item, { kind: "permission" }>;

export interface PermissionsDeps {
  /** True when the chat view is on screen (WebviewView.visible). */
  visible(): boolean;
  /** A non-modal notification with one button per option name; undefined when dismissed. */
  notify(message: string, ...names: string[]): Thenable<string | undefined>;
  log: Log;
}

interface Pending {
  card: Card;
  resolve(r: RequestPermissionResponse): void;
}

const CANCELLED: RequestPermissionResponse = { outcome: { outcome: "cancelled" } };

export class Permissions {
  private next = 0;
  /** Insertion order is request order: the first key is the oldest. */
  private readonly pending = new Map<string, Pending>();

  constructor(
    private readonly conv: Conversation,
    private readonly deps: PermissionsDeps,
  ) {}

  request(r: RequestPermissionRequest): Promise<RequestPermissionResponse> {
    const requestId = `p${++this.next}`;
    const card: Card = {
      kind: "permission",
      requestId,
      title: r.toolCall.title ?? "",
      options: r.options.map((o) => ({ optionId: o.optionId, name: o.name, kind: o.kind })),
    };
    if (r.toolCall.toolCallId) {
      card.toolCallId = r.toolCall.toolCallId;
    }
    const answer = new Promise<RequestPermissionResponse>((resolve) => {
      this.pending.set(requestId, { card, resolve });
    });
    this.conv.addItem(card);
    this.deps.log.info(`session/request_permission ${requestId} "${card.title}"`);
    if (!this.deps.visible()) {
      this.notify(requestId, card);
    }
    return answer;
  }

  /** A click on a card (or a notification button); ignored once the request is closed. */
  answer(requestId: string, optionId: string): void {
    const p = this.pending.get(requestId);
    if (!p || !p.card.options.some((o) => o.optionId === optionId)) {
      return;
    }
    this.pending.delete(requestId);
    p.card.answered = optionId;
    this.conv.changed();
    this.deps.log.info(`session/request_permission ${requestId} answered ${optionId}`);
    p.resolve({ outcome: { outcome: "selected", optionId } });
  }

  /** Stop or tenon gone: every open request is answered "cancelled". */
  cancelAll(): void {
    for (const id of [...this.pending.keys()]) {
      this.cancel(id);
    }
  }

  /** Open requests, oldest first. */
  pendingIds(): string[] {
    return [...this.pending.keys()];
  }

  private cancel(requestId: string): void {
    const p = this.pending.get(requestId);
    if (!p) {
      return;
    }
    this.pending.delete(requestId);
    p.card.refused = true;
    this.conv.changed();
    this.deps.log.info(`session/request_permission ${requestId} cancelled`);
    p.resolve(CANCELLED);
  }

  /**
   * Notifications cannot be closed from the extension: once the card has
   * answered, a late choice there is ignored (the request is closed).
   */
  private notify(requestId: string, card: Card): void {
    const names = card.options.map((o) => o.name);
    const message = vscode.l10n.t("tenon asks for permission: {0}", card.title);
    void Promise.resolve(this.deps.notify(message, ...names)).then(
      (choice) => {
        const option = choice === undefined ? undefined : card.options.find((o) => o.name === choice);
        if (option) {
          this.answer(requestId, option.optionId);
        } else {
          this.cancel(requestId);
        }
      },
      () => this.cancel(requestId),
    );
  }
}
