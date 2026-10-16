import '../../../core/error/app_failure.dart';

const maximumBatchMessages = 20;

/// The selection/confirmation/picker carries only IDs and an immutable lease.
/// Message bodies, URLs and protocol snapshots never become selection state.
final class ChatBatchIntent {
  ChatBatchIntent({
    required this.conversationId,
    required this.generation,
    required Iterable<String> messageIds,
    required this._check,
  }) : messageIds = List.unmodifiable(messageIds) {
    if (this.messageIds.isEmpty ||
        this.messageIds.length > maximumBatchMessages ||
        this.messageIds.any((id) => id.isEmpty) ||
        this.messageIds.toSet().length != this.messageIds.length) {
      throw const AppFailure(FailureKind.invalidInput);
    }
  }
  final String conversationId;
  final int generation;
  final List<String> messageIds;
  final void Function() _check;
  void check() => _check();
  bool get current {
    try {
      check();
      return true;
    } on AppFailure {
      return false;
    }
  }

  @override
  String toString() => 'ChatBatchIntent';
}

/// ACK means the SDK request completed. It is not a physical-erasure receipt.
final class BatchDeleteResult {
  BatchDeleteResult({
    required Iterable<String> acknowledged,
    required Iterable<String> notAttempted,
    this.unconfirmedId,
    this.stopped = false,
  }) : acknowledged = List.unmodifiable(acknowledged),
       notAttempted = List.unmodifiable(notAttempted);
  final List<String> acknowledged, notAttempted;
  final String? unconfirmedId;
  final bool stopped;
  bool get complete =>
      !stopped && unconfirmedId == null && notAttempted.isEmpty;
  String get notice => complete
      ? '已完成 ${acknowledged.length} 条删除请求，请刷新核对记录。'
      : unconfirmedId != null
      ? '已完成 ${acknowledged.length} 条删除请求；1 条结果未确认，已停止，剩余 ${notAttempted.length} 条未提交。请先刷新核对，不会自动重试。'
      : '已完成 ${acknowledged.length} 条删除请求；消息或会话已变化，已停止，${notAttempted.length} 条未提交。';
  @override
  String toString() => 'BatchDeleteResult';
}
