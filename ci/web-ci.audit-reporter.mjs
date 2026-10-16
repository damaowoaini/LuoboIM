// Vitest 5 public reporter API. JSON alone omits unhandled errors, so retain
// this separate completion/hook audit alongside the built-in JSON and JUnit.
import { writeFileSync } from "node:fs";

export default class WebCIAuditReporter {
  onTestRunEnd(modules, unhandledErrors, reason) {
    const error = (value) => ({ name: String(value?.name ?? "Error"), message: String(value?.message ?? "") });
    const suites = modules.flatMap((module) => [module, ...module.children.allSuites()]);
    const tests = modules.flatMap((module) => [...module.children.allTests()].map((test) => ({
      id: test.id,
      name: test.name,
      fullName: test.fullName,
      state: test.result().state,
      mode: test.options.mode,
      errors: (test.result().errors ?? []).map(error),
      hooks: test.task.result?.hooks ?? {},
    })));
    const output = process.env.WEB_CI_AUDIT_FILE;
    if (!output) throw new Error("WEB_CI_AUDIT_FILE is required");
    writeFileSync(output, JSON.stringify({
      reason,
      unhandledErrors: unhandledErrors.map(error),
      suiteErrors: suites.flatMap((suite) => suite.errors().map(error)),
      failedHooks: [...suites, ...modules.flatMap((module) => [...module.children.allTests()])]
        .flatMap((item) => Object.entries(item.task.result?.hooks ?? {})
          .filter(([, state]) => state === "fail").map(([name]) => ({ id: item.id, name }))),
      tests,
    }, null, 2) + "\n", { flag: "wx" });
  }
}
