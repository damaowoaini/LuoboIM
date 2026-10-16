enum SessionCleanupWarning { pending, unconfirmed }

enum FailureKind {
  network,
  timeout,
  cancelled,
  sessionExpired,
  rateLimited,
  permissionDenied,
  invalidInput,
  configuration,
  invalidResponse,
  storage,
  localStorage,
  dataChanged,
  logoutPending,
  logoutUnconfirmed,
  sessionUpgrade,
  unknown,
}

/// Carries no raw response body, URL, password or token into the presentation
/// layer. Server text is not safe to display or write to a crash report.
final class AppFailure implements Exception {
  const AppFailure(this.kind, {this.code, this.cleanupWarning});
  final FailureKind kind;
  final int? code;
  final SessionCleanupWarning? cleanupWarning;

  AppFailure withCleanup(SessionCleanupWarning warning) =>
      AppFailure(kind, code: code, cleanupWarning: warning);

  factory AppFailure.fromCode(int code) {
    if (code >= 1501 && code <= 1507) {
      return AppFailure(FailureKind.sessionExpired, code: code);
    }
    if (code == 20018) return AppFailure(FailureKind.rateLimited, code: code);
    if (code == 1002 || code == 20013) {
      return AppFailure(FailureKind.permissionDenied, code: code);
    }
    return AppFailure(FailureKind.unknown, code: code);
  }

  String get message {
    final primary = switch (kind) {
      FailureKind.network => '网络连接失败，请检查网络后重试',
      FailureKind.timeout => '请求超时，请稍后重试',
      FailureKind.cancelled => '操作已取消',
      FailureKind.sessionExpired => '登录已失效，请重新登录',
      FailureKind.rateLimited => '尝试次数过多，请稍后再试',
      FailureKind.permissionDenied => '权限不足，请刷新后重试',
      FailureKind.invalidInput => '请检查填写的信息',
      FailureKind.configuration => '服务配置不完整，请联系服务管理员',
      FailureKind.invalidResponse => '服务返回的数据不完整，请稍后重试',
      FailureKind.storage => '无法安全保存登录信息，请重试',
      FailureKind.localStorage => '无法读取或保存本地资料，请重试',
      FailureKind.dataChanged => '资料在操作期间发生变化，请刷新后重试',
      FailureKind.logoutPending => '已退出本机，服务端正在完成此会话的连接撤销',
      FailureKind.logoutUnconfirmed => '已退出本机，暂时无法确认服务端是否已撤销此会话',
      FailureKind.sessionUpgrade => '登录信息需要更新，请重新登录；原本地资料已保留',
      FailureKind.unknown => '操作未完成，请稍后重试',
    };
    return switch (cleanupWarning) {
      SessionCleanupWarning.pending => '$primary；本次登录会话正在撤销',
      SessionCleanupWarning.unconfirmed => '$primary；暂未确认本次登录会话已撤销',
      null => primary,
    };
  }

  @override
  String toString() => 'AppFailure(${kind.name}, code: $code)';
}
