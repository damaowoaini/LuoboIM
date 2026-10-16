import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { pipeline } from "node:stream/promises";
import { isWithinRoot, safeFilename } from "./policy";

export async function exportAttachment(options: {
  sourcePath: string;
  filename?: string;
  roots: string[];
  assertOwner(): void;
  chooseDestination(filename: string): Promise<string | undefined>;
}): Promise<{ saved: boolean; canceled: boolean }> {
  options.assertOwner();
  if (!isWithinRoot(options.sourcePath, options.roots))
    throw new Error("文件不属于当前账号缓存");
  const source = fs.realpathSync(options.sourcePath);
  if (!fs.statSync(source).isFile()) throw new Error("只能另存附件文件");
  const filename = safeFilename(options.filename || path.basename(source)).slice(0, 200);
  const destination = await options.chooseDestination(filename);
  options.assertOwner();
  if (!destination) return { saved: false, canceled: true };
  if (!isWithinRoot(source, options.roots)) throw new Error("附件缓存已变更");
  const parent = fs.realpathSync(path.dirname(destination));
  const target = path.join(parent, path.basename(destination));
  if (source === target) return { saved: true, canceled: false };
  const temporary = path.join(parent, `.luobo-export-${randomUUID()}.tmp`);
  try {
    // The renderer cannot choose a destination: only the native save dialog can.
    await pipeline(fs.createReadStream(source), fs.createWriteStream(temporary, {
      flags: "wx", mode: 0o600,
    }));
    options.assertOwner();
    // Commit synchronously so no account-change event can run between the owner
    // check and the atomic rename. File contents were streamed asynchronously.
    fs.renameSync(temporary, target);
    return { saved: true, canceled: false };
  } finally {
    await fs.promises.rm(temporary, { force: true }).catch(() => undefined);
  }
}
