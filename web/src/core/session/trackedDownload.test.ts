import { describe, expect, it, vi } from "vitest";
import { startTrackedDownload } from "./trackedDownload";

describe("native download acknowledgement ordering", () => {
  it("retains a completion delivered before the IPC acknowledgement", async () => {
    const tasks = new Map<string, { clientMsgID: string; state: string }>();
    await startTrackedDownload({
      register: () => tasks.set("url", { clientMsgID: "original-message", state: "downloading" }),
      invoke: async () => {
        // A small or cached file may finish while the invoke reply is in flight.
        const task = tasks.get("url");
        expect(task?.clientMsgID).toBe("original-message");
        task!.state = "finish";
      },
      assertCurrent: () => undefined,
      isCurrent: () => true,
      rollback: () => tasks.delete("url"),
    });
    expect(tasks.get("url")?.state).toBe("finish");
  });

  it("removes a failed start only while its owner is current", async () => {
    let current = true;
    const rollback = vi.fn();
    await expect(startTrackedDownload({
      register: () => undefined,
      invoke: async () => { throw new Error("native rejected"); },
      assertCurrent: () => undefined,
      isCurrent: () => current,
      rollback,
    })).rejects.toThrow("native rejected");
    expect(rollback).toHaveBeenCalledOnce();
    rollback.mockClear();
    await expect(startTrackedDownload({
      register: () => undefined,
      invoke: async () => { current = false; throw new Error("late old failure"); },
      assertCurrent: () => undefined,
      isCurrent: () => current,
      rollback,
    })).rejects.toThrow("late old failure");
    expect(rollback).not.toHaveBeenCalled();
  });
});
