import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/privacy/message_privacy.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../chat/data/message_mapper.dart';
import '../domain/conversation.dart';

final class ConversationRepository {
  ConversationRepository(this.gateway, this.session)
    : _generation = session.state.generation,
      _interactiveRevision = session.interactiveRevision,
      _accountKey = session.state.credentials?.accountKey;
  final OpenImGateway gateway;
  final SessionCoordinator session;
  final int _generation;
  final int _interactiveRevision;
  bool get current =>
      session.isInteractiveCurrent(_generation, _interactiveRevision);
  final String? _accountKey;

  int get generation {
    final state = session.state;
    if (!current ||
        state.phase != SessionPhase.online ||
        state.generation != _generation ||
        _accountKey == null ||
        state.credentials?.accountKey != _accountKey) {
      throw const AppFailure(FailureKind.sessionExpired);
    }
    return _generation;
  }

  Future<List<Conversation>> list({int offset = 0, int count = 40}) {
    final epoch = generation;
    return gateway.runForHistory(epoch, (manager, access) async {
      final revisions = gateway.conversationHistorySnapshot;
      final items = await manager.conversationManager.getConversationListSplit(
        offset: offset,
        count: count,
      );
      access.check();
      return items
          .map(
            (item) =>
                (gateway.isConversationHistoryClearing(item.conversationID) ||
                    gateway.conversationHistoryRevision(item.conversationID) !=
                        (revisions[item.conversationID] ?? 0))
                ? mapConversation(item, includePreview: false)
                : mapConversation(item, ledger: gateway.privacyLedger),
          )
          .toList(growable: false);
    });
  }

  /// One complete SDK inbox snapshot, independent of the UI's loaded pages.
  /// The original owner/history/CID cut is retained through subsequent layout.
  Future<UnreadConversationSnapshot> unreadSnapshot({required bool Function() sourceCurrent}) {
    final epoch = generation;
    final owner = session.state.credentials;
    final interactive = session.interactiveRevision;
    final history = gateway.historyAccess(epoch);
    final revisions = gateway.conversationHistorySnapshot;
    void check() {
      if (!current || !sourceCurrent() || owner == null ||
          !identical(session.state.credentials, owner) ||
          !session.isInteractiveCurrent(epoch, interactive)) {
        throw const AppFailure(FailureKind.cancelled);
      }
      history.check();
    }
    bool cidCurrent(String id) {
      try {
        check();
        if (id.isEmpty || id.length > 512 || gateway.isConversationHistoryClearing(id) ||
            gateway.conversationHistoryRevision(id) != (revisions[id] ?? 0)) {
          return false;
        }
        // Admission only; never substitute this for the original cut above.
        gateway.historyAccess(epoch, conversationId: id).check();
        return true;
      } on AppFailure { return false; }
    }
    check();
    return gateway.runForHistory(epoch, (manager, access) async {
      check();
      final result = await manager.conversationManager.getAllConversationList();
      check();
      final seen = <String>{};
      final mapped = <Conversation>[];
      for (final item in result) {
        if (item.conversationID.isEmpty || item.conversationID.length > 512 ||
            !seen.add(item.conversationID)) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
        final allowed = cidCurrent(item.conversationID);
        mapped.add(allowed ? mapConversation(item, ledger: gateway.privacyLedger)
            : mapConversation(item, includePreview: false));
      }
      check();
      return UnreadConversationSnapshot(List.unmodifiable(mapped), check, cidCurrent);
    }, access: history);
  }

  Future<Conversation> get(String id) => gateway.runForHistory(generation, (
    manager,
    access,
  ) async {
    final items = await manager.conversationManager.getMultipleConversation(
      conversationIDList: [id],
    );
    final matches = items.where((item) => item.conversationID == id);
    if (matches.isEmpty) throw const AppFailure(FailureKind.invalidResponse);
    access.check();
    return mapConversation(matches.first, ledger: gateway.privacyLedger);
  }, conversationId: id);

  Future<void> pin(Conversation item) =>
      gateway.runForHistory(generation, (manager, access) async {
        await manager.conversationManager.pinConversation(
          conversationID: item.id,
          isPinned: !item.pinned,
        );
      });

  Future<void> mute(Conversation item) =>
      gateway.runForHistory(generation, (manager, access) async {
        await manager.conversationManager.setConversationRecvMessageOpt(
          conversationID: item.id,
          status: item.muted ? 0 : 2,
        );
      });

  Future<void> markRead(String id) =>
      gateway.runForHistory(generation, (manager, access) async {
        await manager.conversationManager.markConversationMessageAsRead(
          conversationID: id,
        );
      });

  Future<void> hide(String id) =>
      gateway.runForHistory(generation, (manager, access) async {
        await manager.conversationManager.hideConversation(conversationID: id);
      });

  Future<void> deleteWithMessages(String id) => gateway
      .clearConversationMessages(generation, id, removeConversation: true);
}

Conversation mapConversation(
  ConversationInfo item, {
  MessagePrivacyLedger? ledger,
  DateTime? now,
  bool includePreview = true,
}) {
  final privacy = !includePreview || item.latestMsg == null
      ? null
      : ledger?.observe(
          item.conversationID,
          item.latestMsg!.toJson(),
          now: now ?? DateTime.now(),
        );
  return Conversation(
    id: item.conversationID,
    title: item.showName ?? item.userID ?? item.groupID ?? '会话',
    preview: includePreview
        ? messagePreview(item.latestMsg, privacy: privacy)
        : '',
    privacy: privacy,
    updatedAt: DateTime.fromMillisecondsSinceEpoch(
      (item.draftTextTime ?? 0) > (item.latestMsgSendTime ?? 0)
          ? item.draftTextTime!
          : item.latestMsgSendTime ?? 0,
    ),
    unread: item.unreadCount.clamp(0, 1 << 30),
    pinned: item.isPinned ?? false,
    muted: item.recvMsgOpt != null && item.recvMsgOpt != 0,
    isGroup: (item.groupID ?? '').isNotEmpty,
    canSend:
        item.isNotInGroup != true &&
        ((item.userID ?? '').isNotEmpty || (item.groupID ?? '').isNotEmpty),
    avatar: secureMediaUri(item.faceURL),
    userId: item.userID,
    groupId: item.groupID,
    draft: item.draftText ?? '',
    mentions: (item.groupAtType ?? 0) != 0,
  );
}

/// Ephemeral read ticket; not a serialized store and never renewed after clear.
final class UnreadConversationSnapshot {
  const UnreadConversationSnapshot(this.items, this.check, this.cidCurrent);
  final List<Conversation> items;
  final void Function() check;
  final bool Function(String id) cidCurrent;
}
