import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/tenant/session_access.dart';
import '../../chat/data/message_mapper.dart';
import '../../chat/domain/chat_message.dart';
import '../../contacts/data/contacts_repository.dart';
import '../../contacts/domain/contact.dart';
import '../../group/domain/group.dart';
import '../domain/global_search.dart';

final class GlobalSearchQuery {
  GlobalSearchQuery._(this.keyword, this.timePosition, this.history,
      this.historyFailure, this.revisions, this._ownerCurrent);
  final String keyword;
  final int timePosition;
  final MessageHistoryAccess? history;
  final AppFailure? historyFailure;
  final Map<String, int> revisions;
  final bool Function() _ownerCurrent;
  void checkOwner() {
    if (!_ownerCurrent()) throw const AppFailure(FailureKind.cancelled);
  }
  void checkHistory() {
    checkOwner();
    if (history == null) {
      throw historyFailure ?? const AppFailure(FailureKind.cancelled);
    }
    history!.check();
  }
}

final class GlobalSearchRepository {
  GlobalSearchRepository(this.gateway, this.access, this.contacts,
      {DateTime Function()? now}) : now = now ?? DateTime.now;
  final OpenImGateway gateway;
  final SessionAccess access;
  final ContactsRepository contacts;
  final DateTime Function() now;
  final _removed = <String>{};
  bool _disposed = false;
  bool get current => !_disposed && access.isCurrent &&
      identical(access.session.state.credentials, access.credentials);

  GlobalSearchQuery begin(String raw, bool Function() queryCurrent) {
    final keyword = raw.trim();
    if (!current || keyword.isEmpty || keyword.length > 200) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    MessageHistoryAccess? history;
    AppFailure? failure;
    try {
      history = gateway.historyAccess(access.generation);
    } on AppFailure catch (error) {
      failure = error;
    }
    // Captured before either native message request, including unknown CIDs.
    return GlobalSearchQuery._(keyword, now().millisecondsSinceEpoch ~/ 1000,
        history, failure, gateway.conversationHistorySnapshot,
        () => current && queryCurrent());
  }

  bool cidCurrent(GlobalSearchQuery query, String id) {
    try {
      query.checkHistory();
      final sameCut = id.isNotEmpty && id.length <= 512 &&
          !gateway.isConversationHistoryClearing(id) &&
          gateway.conversationHistoryRevision(id) == (query.revisions[id] ?? 0);
      if (!sameCut) return false;
      // Admission only, including a timed-out/quarantined CID. This probe is
      // never stored or substituted for the original query/revision checks.
      gateway.historyAccess(access.generation, conversationId: id).check();
      return true;
    } on AppFailure {
      return false;
    }
  }

  Future<List<Contact>> friends(GlobalSearchQuery query) async {
    query.checkOwner();
    final values = await contacts.friends();
    query.checkOwner();
    final word = query.keyword.toLowerCase();
    return List.unmodifiable(values.where((item) =>
        [item.id, item.name, item.remark].any((s) => s.toLowerCase().contains(word))));
  }

  Future<List<GroupSummary>> groups(GlobalSearchQuery query) async {
    query.checkOwner();
    final values = await contacts.groups();
    query.checkOwner();
    final word = query.keyword.toLowerCase();
    return List.unmodifiable(values.where((item) =>
        [item.id, item.name].any((s) => s.toLowerCase().contains(word))));
  }

  Future<List<GlobalMessageHit>> messages(
      GlobalSearchQuery query, GlobalSearchKind kind) async {
    if (kind != GlobalSearchKind.text && kind != GlobalSearchKind.files) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    query.checkHistory();
    final types = kind == GlobalSearchKind.files
        ? [MessageType.file] : [MessageType.text, MessageType.atText];
    return gateway.runForHistory(access.generation, (manager, lease) async {
      final result = await manager.messageManager.searchLocalMessages(
        conversationID: null,
        keywordList: [query.keyword],
        keywordListMatchType: 1,
        messageTypeList: types,
        searchTimePosition: query.timePosition,
        pageIndex: 1,
        count: 40,
      );
      query.checkHistory();
      lease.check();
      if (result.searchResultItems == null && (result.totalCount ?? 0) > 0) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      final values = <String, GlobalMessageHit>{};
      for (final group in result.searchResultItems ?? <SearchResultItems>[]) {
        final cid = group.conversationID;
        if (cid == null || cid.isEmpty || cid.length > 512 ||
            group.messageList == null && (group.messageCount ?? 0) > 0) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
        if (!cidCurrent(query, cid)) continue;
        for (final raw in group.messageList ?? <Message>[]) {
          query.checkHistory();
          // In particular do not teach the ledger about an unknown ID returned
          // after this CID was cleared. No fresh lease is acquired here.
          if (!cidCurrent(query, cid)) break;
          final id = raw.clientMsgID;
          if (id == null || id.isEmpty || id.length > 512 ||
              _removed.contains(id) || raw.status == MessageStatus.deleted ||
              !types.contains(raw.contentType)) continue;
          final privacy = gateway.privacyLedger.observe(cid, raw.toJson(), now: now());
          final item = mapMessage(raw, access.credentials.userId, privacy: privacy);
          final hit = GlobalMessageHit(
            conversationId: cid, title: group.showName ?? cid, message: item,
            isCurrent: () => !_removed.contains(id) && cidCurrent(query, cid),
          );
          values[hit.key] = hit;
        }
      }
      final sorted = values.values.toList()..sort(compareHits);
      // Native global search on the audited core ignores page/count. This is
      // one full snapshot, never a purported first SDK page or a truncated cap.
      return List.unmodifiable(sorted);
    }, access: query.history);
  }

  static int compareHits(GlobalMessageHit a, GlobalMessageHit b) {
    final time = b.message.sentAt.compareTo(a.message.sentAt);
    return time == 0 ? a.key.compareTo(b.key) : time;
  }

  GlobalSourceSelection selection(GlobalMessageHit hit) => GlobalSourceSelection(
    conversationId: hit.conversationId, messageId: hit.message.id,
    isCurrent: () => current && hit.visibleAt(now()),
  );

  Future<ChatMessage> readExact(GlobalMessageHit hit) async {
    if (!current || !hit.visibleAt(now())) {
      throw const AppFailure(FailureKind.cancelled);
    }
    final lease = gateway.historyAccess(access.generation,
        conversationId: hit.conversationId);
    return gateway.runForHistory(access.generation, (manager, access) async {
      final result = await manager.messageManager.findMessageList(searchParams: [
        SearchParams(conversationID: hit.conversationId,
            clientMsgIDList: [hit.message.id]),
      ]);
      lease.check();
      if (!current || !hit.visibleAt(now())) {
        throw const AppFailure(FailureKind.cancelled);
      }
      ChatMessage? found;
      for (final group in [...?result.findResultItems, ...?result.searchResultItems]) {
        if (group.conversationID != hit.conversationId) continue;
        for (final raw in group.messageList ?? <Message>[]) {
          if (raw.clientMsgID != hit.message.id || raw.status == MessageStatus.deleted) continue;
          final privacy = gateway.privacyLedger.observe(
              hit.conversationId, raw.toJson(), now: now());
          found = mapMessage(raw, this.access.credentials.userId, privacy: privacy);
        }
      }
      if (found == null || !found.privacy.canViewAt(now())) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      return found;
    }, access: lease);
  }

  void forget(Iterable<String> ids) {
    _removed.addAll(ids);
    gateway.privacyLedger.forget(ids);
  }
  void dispose() { _disposed = true; }
}
