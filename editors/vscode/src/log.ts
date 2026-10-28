// Output channel "tenon": extension log lines and the child's stderr.
import * as vscode from "vscode";

export interface Log {
  /** One line from the extension, timestamped. */
  info(message: string): void;
  /** Raw stderr text from the tenon child, appended as is. */
  stderr(text: string): void;
}

export class OutputLog implements Log, vscode.Disposable {
  private readonly channel = vscode.window.createOutputChannel("tenon");

  info(message: string): void {
    this.channel.appendLine(`[${new Date().toISOString()}] ${message}`);
  }

  stderr(text: string): void {
    this.channel.append(text);
  }

  show(): void {
    this.channel.show(true);
  }

  dispose(): void {
    this.channel.dispose();
  }
}
