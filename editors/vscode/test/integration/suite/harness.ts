// Minimal sequential test harness for the extension host (no mocha): test
// files register cases with `test`, index.ts runs them in order.

type Fn = () => Promise<void> | void;

interface Case {
  name: string;
  fn: Fn;
  timeout: number;
}

const cases: Case[] = [];

export function test(name: string, fn: Fn, timeout = 30_000): void {
  cases.push({ name, fn, timeout });
}

export async function runAll(): Promise<number> {
  let failed = 0;
  for (const c of cases) {
    const start = Date.now();
    let timer: NodeJS.Timeout | undefined;
    try {
      await Promise.race([
        Promise.resolve().then(c.fn),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new Error(`timeout after ${c.timeout} ms`)), c.timeout);
        }),
      ]);
      console.log(`  ok   ${c.name} (${Date.now() - start} ms)`);
    } catch (err) {
      failed++;
      console.error(`  FAIL ${c.name}\n       ${err instanceof Error ? (err.stack ?? err.message) : String(err)}`);
    } finally {
      clearTimeout(timer);
    }
  }
  console.log(`${cases.length - failed} passed, ${failed} failed`);
  return failed;
}

export function assert(cond: unknown, message: string): asserts cond {
  if (!cond) {
    throw new Error(message);
  }
}

/** Polls cond every 50 ms until it returns a truthy value or ms elapse. */
export async function waitFor<T>(cond: () => T | undefined | false, ms: number, what: string): Promise<T> {
  const deadline = Date.now() + ms;
  for (;;) {
    const v = cond();
    if (v) {
      return v;
    }
    if (Date.now() > deadline) {
      throw new Error(`timed out after ${ms} ms waiting for ${what}`);
    }
    await new Promise((r) => setTimeout(r, 50));
  }
}
