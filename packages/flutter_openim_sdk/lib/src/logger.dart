/// Content-free SDK events, delivered only when the application opts in.
enum SdkDiagnosticEvent { callbackFailed, loginProfileFailed }

class SdkDiagnostics {
  static void Function(SdkDiagnosticEvent event)? onEvent;

  static void report(SdkDiagnosticEvent event) {
    try {
      onEvent?.call(event);
    } catch (_) {
      // Diagnostics must never change SDK control flow.
    }
  }
}

/// Retained for source compatibility. Arbitrary SDK payload logging is disabled.
class Logger {
  @Deprecated('Use SdkDiagnostics.onEvent for content-free diagnostics')
  static void print(String text) {}
}
