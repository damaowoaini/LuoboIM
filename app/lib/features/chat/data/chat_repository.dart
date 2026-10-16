import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../conversation/data/conversation_repository.dart';
import '../../conversation/domain/conversation.dart';
import '../domain/chat_message.dart';
import '../domain/chat_batch.dart';
import '../domain/chat_mention.dart';
import '../domain/chat_snapshot.dart';
import '../domain/saved_emoji.dart';
import '../domain/location_pick.dart';
import '../domain/chat_card.dart';
import '../domain/chat_search.dart';
import '../domain/message_privacy.dart';
import 'message_mapper.dart';
import 'group_mention_authority.dart';
import 'retained_merge_codec.dart';

final class ChatRepository {
  ChatRepository({
    required this.gateway,
    required this.session,
    required this.conversationId,
    this.api,
    DateTime Function()? now,
  }) : now = now ?? DateTime.now,
       generation = session.state.generation,
       interactiveRevision = session.interactiveRevision,
       _userId = session.state.credentials?.userId ?? '',
       _cacheRevision = gateway.historyRevision;

  final OpenImGateway gateway;
  final SessionCoordinator session;
  final String conversationId;
  final TenantApi? api;
  final int generation;
  final int interactiveRevision;
  bool get current =>
      session.isInteractiveCurrent(generation, interactiveRevision);
  final DateTime Function() now;
  final String _userId;
  final Map<String, Message> _raw = {};
  final Map<String, String> _preparedQuoteSources = {};
  // A late native snapshot cannot undo a removal already observed in this
  // repository's frozen account generation. This does not rewrite the SDK DB.
  final Set<String> _removedIds = {};
  final Map<String, SavedEmoji> _preparedFaces = {};
  final Map<String, ({String? userId, String? groupId, bool group})>
  _preparedLocations = {};
  final Map<String, ({String? userId, String? groupId, bool group})>
  _preparedCards = {};
  int _emojiPolicyRevision = 0;
  void invalidateSavedEmojiProofs() {
    ++_emojiPolicyRevision;
  }

  bool isRemoved(String id) => _removedIds.contains(id);
  final Map<String, ({String path, String? thumbnail})> _staged = {};
  static int _mediaSerial = 0;
  Conversation? _conversation;
  final Set<String> _groupReadMarked = {};
  Future<void>? _readOperation;
  Future<void>? _visibleReadOperation;
  final Set<String> _visibleReadQueue = {};
  int _cacheRevision;

  void resetHistory() {
    _cacheRevision = gateway.historyRevision;
    _conversation = _conversation?.withoutMessagePreview();
    _groupReadMarked.clear();
    _visibleReadQueue.clear();
    _raw.clear();
    _preparedFaces.clear();
    _preparedLocations.clear();
    _preparedCards.clear();
    _preparedQuoteSources.clear();
    _staged.clear();
  }

  ChatMessage _map(Message message) {
    final item = mapMessage(
      message,
      _userId,
      privacy: gateway.privacyLedger.observe(
        conversationId,
        message.toJson(),
        now: now(),
      ),
    );
    final local = _staged[item.id];
    return local == null
        ? item
        : item.withLocalMedia(local.path, snapshot: local.thumbnail);
  }

  MessagePrivacy privacyFor(ChatMessage item) =>
      gateway.privacyLedger.lookup(item.id);
  bool canView(ChatMessage item) =>
      current && !isRemoved(item.id) && privacyFor(item).canViewAt(now());
  bool canRetain(ChatMessage item) =>
      canView(item) && privacyFor(item).canRetainAt(now());
  void requireRetention(String id) {
    if (!current || isRemoved(id)) {
      throw const AppFailure(FailureKind.cancelled);
    }
    if (!gateway.privacyLedger.lookup(id).canRetainAt(now())) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
  }

  Future<T> _run<T>(
    Future<T> Function(IMManager manager, MessageHistoryAccess access)
    operation, {
    MessageHistoryAccess? access,
  }) {
    if (_cacheRevision != gateway.historyRevision) resetHistory();
    if (!current) {
      return Future.error(const AppFailure(FailureKind.cancelled));
    }
    return gateway.runForHistory(
      generation,
      operation,
      access: access,
      conversationId: conversationId,
    );
  }

  Future<Conversation> conversation() async {
    late final MessageHistoryAccess lease;
    final item = await _run((manager, access) async {
      lease = access;
      final result = await manager.conversationManager.getMultipleConversation(
        conversationIDList: [conversationId],
      );
      _checkCurrent(access);
      final matches = result.where(
        (item) => item.conversationID == conversationId,
      );
      if (matches.isEmpty) throw const AppFailure(FailureKind.invalidResponse);
      return mapConversation(
        matches.first,
        ledger: gateway.privacyLedger,
        now: now(),
      );
    });
    _checkCurrent(lease);
    return _conversation = item;
  }

  Future<MessagePage> history({String? beforeId, int lastMinSeq = 0}) => _run((
    manager,
    access,
  ) async {
    final result = await manager.messageManager.getAdvancedHistoryMessageList(
      conversationID: conversationId,
      startMsg: beforeId == null ? null : Message(clientMsgID: beforeId),
      lastMinSeq: lastMinSeq,
      count: 40,
    );
    _checkCurrent(access);
    if ((result.errCode ?? 0) != 0) throw AppFailure.fromCode(result.errCode!);
    final items = <ChatMessage>[];
    for (final item in result.messageList ?? <Message>[]) {
      final id = item.clientMsgID;
      if (id == null ||
          id.isEmpty ||
          isRemoved(id) ||
          item.status == MessageStatus.deleted) {
        continue;
      }
      _raw[id] = item;
      items.add(_map(item));
    }
    return MessagePage(
      messages: items,
      isEnd: result.isEnd ?? items.length < 40,
      lastMinSeq: result.lastMinSeq ?? 0,
    );
  });

  Future<ChatSearchPage> search({
    required String query,
    required ChatHistoryFilter filter,
    required int timePosition,
    int pageIndex = 1,
    int count = 30,
  }) => _run((manager, access) async {
    final keyword = query.trim();
    if (keyword.length > 200 ||
        timePosition <= 0 ||
        pageIndex < 1 ||
        count < 1 ||
        count > 100 ||
        (filter == ChatHistoryFilter.all && keyword.isEmpty) ||
        (filter == ChatHistoryFilter.media && keyword.isNotEmpty)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final types = switch (filter) {
      ChatHistoryFilter.all => <int>[],
      ChatHistoryFilter.text => [MessageType.text, MessageType.atText],
      ChatHistoryFilter.media => [MessageType.picture, MessageType.video],
      ChatHistoryFilter.file => [MessageType.file],
    };
    final result = await manager.messageManager.searchLocalMessages(
      conversationID: conversationId,
      keywordList: keyword.isEmpty ? const [] : [keyword],
      keywordListMatchType: 1,
      messageTypeList: types,
      searchTimePosition: timePosition,
      pageIndex: pageIndex,
      count: count,
    );
    _checkCurrent(access);
    final messages = <String, ChatMessage>{};
    var rawCount = 0;
    for (final group in result.searchResultItems ?? <SearchResultItems>[]) {
      if (group.conversationID != conversationId) continue;
      for (final message in group.messageList ?? <Message>[]) {
        rawCount++;
        access.check();
        final id = message.clientMsgID;
        if (id == null ||
            id.isEmpty ||
            isRemoved(id) ||
            message.status == MessageStatus.deleted ||
            (types.isNotEmpty && !types.contains(message.contentType))) {
          continue;
        }
        _raw[id] = message;
        messages[id] = _map(message);
      }
    }
    // Native totalCount describes this page on the audited core, not a total
    // over all pages. Advance by the requested page size until a short page.
    return ChatSearchPage(
      messages: messages.values,
      hasMore: rawCount >= count,
    );
  });

  Future<List<MentionIdentity>> _validateMentions(
    IMManager manager,
    MessageHistoryAccess access,
    Iterable<String> requested,
  ) async {
    final ids = requested.toSet().toList();
    final groupId = _conversation?.groupId;
    if (_conversation?.isGroup != true ||
        groupId == null ||
        groupId.isEmpty ||
        ids.isEmpty ||
        ids.length > maximumMentionRecipients ||
        ids.any((id) => !MentionIdentity(id, id).valid) ||
        manager.conversationManager.atAllTag != mentionAllId) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final server = api;
    if (server == null ||
        server.access.generation != generation ||
        server.access.credentials.accountKey !=
            session.state.credentials?.accountKey ||
        server.access.credentials.userId != _userId) {
      throw const AppFailure(FailureKind.configuration);
    }
    return GroupMentionAuthority(server)
        .verify(groupId: groupId, ids: ids, check: access.check, now: now);
  }

  Future<MentionIdentity> mentionMember(String userId) =>
      _run((manager, access) async {
        if (userId == mentionAllId) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        return (await _validateMentions(manager, access, [userId])).single;
      });

  Future<ChatMessage> prepareText(
    String text, {
    String? quoteId,
    MentionDraft? mentionDraft,
  }) => _run((manager, access) async {
    final actionTime = Stopwatch()..start();
    void checkAction() {
      access.check();
      if (actionTime.elapsed >= const Duration(seconds: 12)) {
        throw const AppFailure(FailureKind.timeout);
      }
    }

    if (text.trim().isEmpty ||
        text.length > 20000 ||
        mentionDraft != null &&
            (!mentionDraft.valid || mentionDraft.text != text)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final conversation = _conversation;
    if (conversation == null || !conversation.canSend) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final quote = quoteId == null ? null : _raw[quoteId];
    if (quoteId != null && quote == null) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (quote != null) requireRetention(quoteId!);
    final selected = mentionDraft?.recipients ?? const <MentionIdentity>[];
    final wire = selected.isEmpty ? text : mentionDraft!.wireText;
    if (wire.length > 20000) throw const AppFailure(FailureKind.invalidInput);
    final recipients = selected.isEmpty
        ? const <MentionIdentity>[]
        : await _validateMentions(
            manager,
            access,
            selected.map((v) => v.userId),
          );
    access.check();
    if (quoteId != null) requireRetention(quoteId);
    checkAction();
    final message = recipients.isNotEmpty
        ? await manager.messageManager.createTextAtMessage(
            text: wire,
            atUserIDList: recipients.map((m) => m.userId).toList(),
            atUserInfoList: recipients
                .map(
                  (m) => AtUserInfo(atUserID: m.userId, groupNickname: m.name),
                )
                .toList(),
            quoteMessage: quote,
          )
        : quote == null
        ? await manager.messageManager.createTextMessage(text: text)
        : await manager.messageManager.createQuoteMessage(
            text: text,
            quoteMsg: quote,
          );
    access.check();
    if (recipients.isNotEmpty) {
      await _validateMentions(manager, access, recipients.map((m) => m.userId));
      access.check();
      final emitted = message.atTextElem;
      if (message.contentType != MessageType.atText ||
          emitted?.text != wire ||
          jsonEncode(emitted?.atUserList) !=
              jsonEncode(recipients.map((m) => m.userId).toList())) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
    }
    checkAction();
    if (quoteId != null) requireRetention(quoteId);
    final id = message.clientMsgID;
    if (id == null || id.isEmpty) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    _raw[id] = message;
    if (quoteId != null) _preparedQuoteSources[id] = quoteId;
    return _map(message).withDelivery(MessageDelivery.sending);
  });

  Future<ChatMessage> prepareAttachment(ChatAttachment attachment) async {
    final lease = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    await _run((_, access) async {
      if (_conversation == null ||
          !_conversation!.canSend ||
          (attachment.kind == AttachmentKind.voice &&
              (attachment.durationSeconds == null ||
                  attachment.durationSeconds! < 1 ||
                  attachment.durationSeconds! > 60))) {
        throw const AppFailure(FailureKind.invalidInput);
      }
    });
    // Stage system-picker/cache files in this account before handing them to
    // the SDK, so failed uploads remain retryable after the picker is closed.
    final source = File(attachment.path);
    final length = await source.length();
    if (length <= 0 || length > 1024 * 1024 * 1024) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    lease.check();
    final directory = await gateway.mediaDirectory(generation);
    lease.check();
    final extensionMatch = RegExp(r'\.[a-zA-Z0-9]{1,10}$')
        .firstMatch(attachment.path);
    final extension = extensionMatch?.group(0) ?? '';
    final stem = '${DateTime.now().microsecondsSinceEpoch}_${++_mediaSerial}';
    final staged = File('${directory.path}/$stem$extension');
    final thumbnail = File('${directory.path}/$stem.thumbnail.jpg');
    try {
      await source.copy(staged.path);
      lease.check();
      Map<String, dynamic>? metadata;
      if (attachment.kind == AttachmentKind.video) {
        metadata = await _run(
          (manager, access) => manager.localVideoMetadata(
            videoPath: staged.path,
            thumbnailPath: thumbnail.path,
          ),
          access: lease,
        );
        if (metadata == null ||
            metadata['durationMilliseconds'] is! num ||
            !(metadata['durationMilliseconds'] as num).isFinite ||
            (metadata['durationMilliseconds'] as num) <= 0 ||
            metadata['thumbnailPath'] != thumbnail.path) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
      }
      // Each asynchronous stage ends at a fresh session barrier. A previous
      // account's file copy must never create a message in a new SDK session.
      final message = await _run(
        (manager, access) => switch (attachment.kind) {
          AttachmentKind.image =>
            manager.messageManager.createImageMessageFromFullPath(
              imagePath: staged.path,
            ),
          AttachmentKind.voice =>
            manager.messageManager.createSoundMessageFromFullPath(
              soundPath: staged.path,
              duration: attachment.durationSeconds ?? 1,
            ),
          AttachmentKind.video =>
            manager.messageManager.createVideoMessageFromFullPath(
              videoPath: staged.path,
              videoType: extension.toLowerCase() == '.mov'
                  ? 'video/quicktime'
                  : 'video/mp4',
              duration: ((metadata!['durationMilliseconds'] as num) / 1000)
                  .ceil()
                  .clamp(1, 1 << 30),
              snapshotPath: metadata['thumbnailPath'] as String,
            ),
          AttachmentKind.file =>
            manager.messageManager.createFileMessageFromFullPath(
              filePath: staged.path,
              fileName: attachment.name.replaceAll(
                RegExp(r'[\x00-\x1f\x7f]'),
                '_',
              ),
            ),
        },
        access: lease,
      );
      lease.check();
      final id = message.clientMsgID;
      if (id == null || id.isEmpty) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      _raw[id] = message;
      _staged[id] = (
        path: staged.path,
        thumbnail: attachment.kind == AttachmentKind.video
            ? thumbnail.path
            : null,
      );
      return _map(message).withDelivery(MessageDelivery.sending);
    } catch (_) {
      if (await staged.exists()) await staged.delete();
      if (await thumbnail.exists()) await thumbnail.delete();
      rethrow;
    }
  }

  /// A reference is intentionally different from a gallery-owned upload receipt.
  /// The existing favorite snapshot contract supplies the original retention gate.
  Future<SavedEmoji> savedEmojiFromMessage(
    String id, {
    required MessageHistoryAccess access,
    required void Function() checkSource,
    void Function(void Function())? onProof,
  }) async {
    access.check();
    checkSource();
    await favoriteSnapshot(id);
    access.check();
    checkSource();
    final raw = _raw[id];
    if (raw == null) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final item = _map(raw);
    _requireEmojiSource(item);
    final Map data;
    if (item.kind == ChatMessageKind.image) {
      final picture =
          raw.pictureElem?.sourcePicture ?? raw.pictureElem?.bigPicture;
      data = {'width': picture?.width, 'height': picture?.height};
    } else {
      final face = raw.faceElem?.data;
      if (face == null) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      final decoded = jsonDecode(face);
      if (decoded is! Map) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      data = decoded;
    }
    if (data['width'] is! int || data['height'] is! int || item.media == null) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final entry = SavedEmoji(
      url: item.media!,
      width: data['width'] as int,
      height: data['height'] as int,
      source: SavedEmojiSource(conversationId, id),
    );
    final proof = await savedEmojiProof(
      entry,
      access: access,
      checkSource: checkSource,
    );
    proof();
    onProof?.call(proof);
    return entry;
  }

  void _requireEmojiSource(ChatMessage item) {
    if (!const {
          ChatMessageKind.image,
          ChatMessageKind.sticker,
        }.contains(item.kind) ||
        item.delivery != MessageDelivery.sent ||
        item.privacy.expiresAtMilliseconds != null ||
        item.privacy.containsPrivateContent ||
        !item.privacy.inspectionComplete ||
        !item.privacy.canRetainAt(now())) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
  }

  /// Returns the exact cut/policy proof used for one rendered item. No fresh
  /// lease is acquired by the returned callback after a clear or route change.
  Future<void Function()> savedEmojiProof(
    SavedEmoji entry, {
    required MessageHistoryAccess access,
    required void Function() checkSource,
  }) {
    access.check();
    checkSource();
    final reference = entry.source;
    final policyRevision = _emojiPolicyRevision;
    final sourceCut = reference == null
        ? null
        : gateway.historyAccess(
            generation,
            conversationId: reference.conversationId,
          );
    return _run((manager, lease) async {
      ChatMessage? original;
      void check() {
        access.check();
        checkSource();
        sourceCut?.check();
        if (reference != null && policyRevision != _emojiPolicyRevision) {
          throw const AppFailure(FailureKind.cancelled);
        }
        if (original != null) {
          _requireEmojiSource(original);
          if (!gateway.privacyLedger.lookup(original.id).canRetainAt(now())) {
            throw const AppFailure(FailureKind.permissionDenied);
          }
          if (reference?.conversationId == conversationId &&
              isRemoved(original.id)) {
            throw const AppFailure(FailureKind.permissionDenied);
          }
        }
      }

      check();
      if (reference != null) {
        final policies = await manager.conversationManager
            .getMultipleConversation(
              conversationIDList: [reference.conversationId],
            );
        check();
        final exact = policies
            .where((p) => p.conversationID == reference.conversationId)
            .toList();
        if (exact.length != 1 ||
            exact.single.isPrivateChat == true ||
            exact.single.isMsgDestruct == true) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
        final result = await manager.messageManager.findMessageList(
          searchParams: [
            SearchParams(
              conversationID: reference.conversationId,
              clientMsgIDList: [reference.messageId],
            ),
          ],
        );
        check();
        final matches = [
          for (final group in [
            ...?result.findResultItems,
            ...?result.searchResultItems,
          ])
            if (group.conversationID == reference.conversationId)
              for (final raw in group.messageList ?? <Message>[])
                if (raw.clientMsgID == reference.messageId) raw,
        ];
        if (matches.length != 1 ||
            matches.single.status != MessageStatus.succeeded) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
        final raw = matches.single;
        // Observe only after the original CID cut has been rechecked.
        original = mapMessage(
          raw,
          _userId,
          privacy: gateway.privacyLedger.observe(
            reference.conversationId,
            raw.toJson(),
            now: now(),
          ),
        );
        if (original.media != entry.url) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
        check();
      }
      return check;
    }, access: access);
  }

  Future<ChatMessage> prepareSavedEmoji(
    SavedEmoji entry, {
    required MessageHistoryAccess access,
    required void Function() checkSource,
  }) async {
    final proof = await savedEmojiProof(
      entry,
      access: access,
      checkSource: checkSource,
    );
    proof();
    return _run((manager, lease) async {
      proof();
      if (_conversation?.canSend != true) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
      final data = jsonEncode(entry.faceData);
      final message = await manager.messageManager.createFaceMessage(
        index: -1,
        data: data,
      );
      proof();
      if (message.clientMsgID?.isNotEmpty != true ||
          message.contentType != MessageType.customFace ||
          message.faceElem?.index != -1 ||
          message.faceElem?.data != data) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      final id = message.clientMsgID!;
      _raw[id] = message;
      _preparedFaces[id] = entry;
      return _map(message).withDelivery(MessageDelivery.sending);
    }, access: access);
  }

  Future<ChatMessage> prepareLocation(
    LocationPick pick, {
    required MessageHistoryAccess access,
    required void Function() checkSource,
  }) => _run((manager, lease) async {
    lease.check();
    checkSource();
    if (_conversation?.canSend != true) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    final original = _conversation!;
    final description = pick.description;
    final message = await manager.messageManager.createLocationMessage(
      latitude: pick.latitude,
      longitude: pick.longitude,
      description: description,
    );
    lease.check();
    checkSource();
    if (_conversation?.canSend != true ||
        _conversation?.userId != original.userId ||
        _conversation?.groupId != original.groupId ||
        _conversation?.isGroup != original.isGroup ||
        message.contentType != MessageType.location ||
        message.clientMsgID?.isNotEmpty != true ||
        message.locationElem?.latitude != pick.latitude ||
        message.locationElem?.longitude != pick.longitude ||
        message.locationElem?.description != description) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    _raw[message.clientMsgID!] = message;
    _preparedLocations[message.clientMsgID!] = (
      userId: original.userId,
      groupId: original.groupId,
      group: original.isGroup,
    );
    return _map(message).withDelivery(MessageDelivery.sending);
  }, access: access);

  Future<ChatMessage> prepareCard(
    String selectedUserId, {
    required MessageHistoryAccess access,
    required void Function() checkSource,
  }) => _run((manager, lease) async {
    lease.check();
    checkSource();
    if (_conversation?.canSend != true) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    final original = _conversation!;
    // Selection is an identity hint. Re-read the complete current friend list
    // after confirmation, before creating a new standard card message.
    final friends = await manager.friendshipManager.getFriendList();
    lease.check();
    checkSource();
    final matches = friends.where((v) => v.userID == selectedUserId).toList();
    if (matches.length != 1) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    final person = matches.single;
    if (person.friendInfo?.userID != selectedUserId ||
        person.blackInfo != null ||
        (person.publicInfo != null &&
            person.publicInfo!.userID != selectedUserId)) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    final card = ChatCard.parse(userId: person.userID, name: person.nickname);
    if (card == null || _conversation?.canSend != true) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    // The sender's local friend remark and avatar/ex metadata are not card
    // payload. Only the freshly read public nickname and exact UID go out.
    final message = await manager.messageManager.createCardMessage(
      userID: card.userId,
      nickname: card.name,
    );
    lease.check();
    checkSource();
    if (_conversation?.canSend != true ||
        _conversation?.userId != original.userId ||
        _conversation?.groupId != original.groupId ||
        _conversation?.isGroup != original.isGroup ||
        message.contentType != MessageType.card ||
        message.clientMsgID?.isNotEmpty != true ||
        message.cardElem?.userID != card.userId ||
        message.cardElem?.nickname != card.name ||
        (message.cardElem?.faceURL ?? '').isNotEmpty ||
        (message.cardElem?.ex ?? '').isNotEmpty) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    _raw[message.clientMsgID!] = message;
    _preparedCards[message.clientMsgID!] = (
      userId: original.userId,
      groupId: original.groupId,
      group: original.isGroup,
    );
    return _map(message).withDelivery(MessageDelivery.sending);
  }, access: access);

  Future<ChatMessage> send(
    String id, {
    MessageHistoryAccess? access,
    void Function()? checkSource,
    void Function()? onDispatched,
  }) => _run((manager, access) async {
    final actionTime = Stopwatch()..start();
    checkSource?.call();
    final message = _raw[id];
    final conversation = _conversation;
    if (message == null || conversation == null || !conversation.canSend) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final locationTarget = _preparedLocations[id];
    if (locationTarget != null &&
        (conversation.userId != locationTarget.userId ||
            conversation.groupId != locationTarget.groupId ||
            conversation.isGroup != locationTarget.group)) {
      throw const AppFailure(FailureKind.cancelled);
    }
    final cardTarget = _preparedCards[id];
    if (cardTarget != null &&
        (conversation.userId != cardTarget.userId ||
            conversation.groupId != cardTarget.groupId ||
            conversation.isGroup != cardTarget.group)) {
      throw const AppFailure(FailureKind.cancelled);
    }
    final face = _preparedFaces[id];
    if (face != null) {
      final proof = await savedEmojiProof(
        face,
        access: access,
        checkSource: checkSource ?? () {},
      );
      proof();
    }
    final mentioned = message.atTextElem?.atUserList ?? const <String>[];
    if (mentioned.isNotEmpty) {
      await _validateMentions(manager, access, mentioned);
      access.check();
      checkSource?.call();
    }
    final policy = _map(message).privacy;
    final sourceId = _preparedQuoteSources[id];
    if (sourceId != null) requireRetention(sourceId);
    if (actualMessageChildren(message.toJson()).isNotEmpty &&
        !policy.canRetainAt(now())) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    if (!policy.canViewAt(now())) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    access.check();
    if (actionTime.elapsed >= const Duration(seconds: 12)) {
      throw const AppFailure(FailureKind.timeout);
    }
    onDispatched?.call();
    final sent = await manager.messageManager.sendMessage(
      message: message,
      userID: conversation.isGroup ? null : conversation.userId,
      groupID: conversation.isGroup ? conversation.groupId : null,
      offlinePushInfo: OfflinePushInfo(
        title: '萝卜对话',
        desc: '你收到一条新消息',
        iOSBadgeCount: true,
      ),
    );
    access.check();
    checkSource?.call();
    if (sent.clientMsgID != id) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    _raw[id] = sent;
    return _map(sent);
  }, access: access);

  void _checkCurrent([MessageHistoryAccess? access]) {
    (access ??
            gateway.historyAccess(generation, conversationId: conversationId))
        .check();
    if (!session.acceptsEvent(generation)) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }

  Future<void> markRead() => _readOperation ??= _markRead().whenComplete(() {
    _readOperation = null;
  });

  Future<void> _markRead() => _run((manager, access) async {
    await manager.conversationManager.markConversationMessageAsRead(
      conversationID: conversationId,
    );
    _checkCurrent(access);
  });

  Future<void> markVisibleRead(Iterable<String> ids) {
    final access = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    _checkCurrent(access);
    if (_conversation?.isGroup != true) return Future.value();
    for (final id in ids) {
      final message = _raw[id];
      if (message != null &&
          message.sendID != _userId &&
          message.status == MessageStatus.succeeded &&
          (message.contentType ?? MessageType.notificationBegin) <
              MessageType.notificationBegin &&
          message.contentType != MessageType.typing &&
          !isLegacyCallSummary(message) &&
          !_groupReadMarked.contains(id)) {
        _visibleReadQueue.add(id);
      }
    }
    if (_visibleReadQueue.isEmpty) return Future.value();
    return _visibleReadOperation ??= _flushVisibleReads().whenComplete(
      () => _visibleReadOperation = null,
    );
  }

  Future<void> _flushVisibleReads() => _run((manager, access) async {
    while (_visibleReadQueue.isNotEmpty) {
      _visibleReadQueue.removeWhere(
        (id) => _groupReadMarked.contains(id) || !_raw.containsKey(id),
      );
      if (_visibleReadQueue.isEmpty) return;
      final chunk = _visibleReadQueue.take(100).toList();
      _visibleReadQueue.removeAll(chunk);
      try {
        _checkCurrent(access);
        await manager.messageManager.sendGroupMessageReadReceipt(
          conversationId,
          chunk,
        );
        _checkCurrent(access);
        _groupReadMarked.addAll(chunk);
      } catch (_) {
        if (session.acceptsEvent(generation) &&
            access.revision == gateway.historyRevision &&
            !gateway.isClearingMessages) {
          _visibleReadQueue.addAll(chunk);
        }
        rethrow;
      }
    }
  });

  Future<MessageReadersPage> readers(
    String messageId, {
    required bool read,
    int offset = 0,
    int count = 30,
  }) => _run((manager, access) async {
    final message = _raw[messageId];
    if (_conversation?.isGroup != true ||
        message?.sendID != _userId ||
        message?.status != MessageStatus.succeeded ||
        offset < 0 ||
        offset > 100000 ||
        count < 1 ||
        count > 100) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final values = await manager.messageManager.getGroupMessageReaderList(
      conversationId,
      messageId,
      filter: read ? 0 : 1,
      offset: offset,
      count: count,
    );
    _checkCurrent(access);
    final result = <MessageReader>[];
    for (final value in values) {
      final id = value.userID;
      if (id == null || id.isEmpty) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      result.add(
        MessageReader(
          userId: id,
          name: value.nickname?.isNotEmpty == true ? value.nickname! : id,
          avatar: secureMediaUri(value.faceURL),
        ),
      );
    }
    return MessageReadersPage(
      readers: result,
      nextOffset: offset + values.length,
      hasMore: values.length >= count,
    );
  });

  Future<ChatFavoriteSnapshot> favoriteSnapshot(String id) =>
      _run((_, access) async {
        final raw = _raw[id];
        if (raw == null || raw.status != MessageStatus.succeeded) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        final message = _map(raw);
        if (!canRetain(message)) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
        final thumbnail = message.thumbnail;
        return ChatFavoriteSnapshot(
          messageId: id,
          conversationId: conversationId,
          content: jsonEncode(raw.toJson()),
          kind: message.kind,
          title: message.text,
          thumbnail: thumbnail != null && thumbnail.scheme == 'https'
              ? thumbnail.toString()
              : '',
        );
      });

  Future<ChatMessage?> findSource(String clientMsgId) async {
    if (clientMsgId.isEmpty) throw const AppFailure(FailureKind.invalidInput);
    if (isRemoved(clientMsgId)) return null;
    final lease = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    final result = await _run(
      (manager, access) => manager.messageManager.findMessageList(
        searchParams: [
          SearchParams(
            conversationID: conversationId,
            clientMsgIDList: [clientMsgId],
          ),
        ],
      ),
      access: lease,
    );
    lease.check();
    for (final group in [
      ...?result.findResultItems,
      ...?result.searchResultItems,
    ]) {
      if (group.conversationID != conversationId) continue;
      for (final message in group.messageList ?? <Message>[]) {
        if (message.clientMsgID == clientMsgId &&
            !isRemoved(clientMsgId) &&
            message.status != MessageStatus.deleted) {
          _raw[clientMsgId] = message;
          return _map(message);
        }
      }
    }
    return null;
  }

  Future<List<ChatMessage>> refreshReadMetadata(Iterable<String> ids) async {
    final lease = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    final requested = ids.where((id) => !isRemoved(id)).toSet().toList();
    final messages = <ChatMessage>[];
    for (var offset = 0; offset < requested.length; offset += 100) {
      lease.check();
      final chunk = requested.skip(offset).take(100).toSet();
      final result = await _run(
        (manager, access) => manager.messageManager.findMessageList(
          searchParams: [
            SearchParams(
              conversationID: conversationId,
              clientMsgIDList: chunk.toList(),
            ),
          ],
        ),
        access: lease,
      );
      lease.check();
      for (final group in [
        ...?result.findResultItems,
        ...?result.searchResultItems,
      ]) {
        if (group.conversationID != conversationId) continue;
        for (final raw in group.messageList ?? <Message>[]) {
          if (!chunk.contains(raw.clientMsgID) ||
              isRemoved(raw.clientMsgID ?? '') ||
              raw.status == MessageStatus.deleted) {
            continue;
          }
          _raw[raw.clientMsgID!] = raw;
          messages.add(_map(raw));
        }
      }
    }
    lease.check();
    return messages.where((item) => !isRemoved(item.id)).toList();
  }

  void forgetMessages(Iterable<String> ids) {
    for (final id in ids) {
      if (id.isEmpty) continue;
      _removedIds.add(id);
      gateway.privacyLedger.forget([id]);
      _raw.remove(id);
      _preparedLocations.remove(id);
      _preparedCards.remove(id);
      _staged.remove(id);
      _visibleReadQueue.remove(id);
      _groupReadMarked.remove(id);
    }
  }

  Future<ChatMessage> forwardMessageJson(
    String content, {
    void Function()? checkSource,
  }) async {
    checkSource?.call();
    final lease = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    if (_conversation == null ||
        !_conversation!.canSend ||
        content.length > 2 * 1024 * 1024) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    late final Message original;
    try {
      var inspectedNodes = 0;
      Object? stripPaths(Object? value, [int depth = 0]) {
        if (depth > 64 || ++inspectedNodes > 32768) {
          throw const FormatException();
        }
        if (value is List) {
          return value.map((item) => stripPaths(item, depth + 1)).toList();
        }
        if (value is Map) {
          return <String, dynamic>{
            for (final entry in value.entries)
              if (entry.key is String)
                entry.key as String:
                    const {
                      'sourcePath',
                      'soundPath',
                      'videoPath',
                      'snapshotPath',
                      'filePath',
                      'localEx',
                    }.contains(entry.key)
                    ? null
                    : stripPaths(entry.value, depth + 1),
          };
        }
        return value;
      }

      final decoded = jsonDecode(content);
      if (decoded is! Map<String, dynamic>) throw const FormatException();
      if (!gateway.privacyLedger
          .observe(conversationId, decoded, now: now())
          .canRetainAt(now())) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
      final json = stripPaths(decoded) as Map<String, dynamic>;
      // A display-only AtText is embedded intact in a new merge root, which
      // owns the destination key. Ordinary forwards still replace the root key.
      if (json['contentType'] != MessageType.atText) json['keyVersion'] = 0;
      original = Message.fromJson(json);
      if ((original.clientMsgID ?? '').isEmpty ||
          original.status != MessageStatus.succeeded) {
        throw const FormatException();
      }
      final mapped = _map(original);
      if (const {
            ChatMessageKind.image,
            ChatMessageKind.voice,
            ChatMessageKind.video,
            ChatMessageKind.file,
          }.contains(mapped.kind) &&
          (mapped.media == null || mapped.media!.scheme != 'https')) {
        throw const FormatException();
      }
      if (mapped.kind == ChatMessageKind.unsupported ||
          mapped.kind == ChatMessageKind.system ||
          mapped.kind == ChatMessageKind.typing) {
        throw const FormatException();
      }
    } on AppFailure {
      rethrow;
    } catch (_) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final forwarded = await _run((manager, access) {
      checkSource?.call();
      // A root AtText would ping the old recipients again. Standard merge
      // keeps its entire protocol payload display-only under a non-At root.
      if (original.contentType == MessageType.atText) {
        return manager.messageManager.createMergerMessage(
          messageList: [original],
          title: '转发的消息',
          summaryList: ['1 条消息（提及仅作原文展示）'],
        );
      }
      return manager.messageManager.createForwardMessage(message: original);
    }, access: lease);
    lease.check();
    checkSource?.call();
    if (!gateway.privacyLedger
        .observe(conversationId, original.toJson(), now: now())
        .canRetainAt(now())) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    final id = forwarded.clientMsgID;
    if (id == null || id.isEmpty || id == original.clientMsgID) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    _raw[id] = forwarded;
    // A separate dispatch fence prevents a completed create from sending after exit.
    return send(id, access: lease, checkSource: checkSource);
  }

  void _checkBatchIntent(ChatBatchIntent intent) {
    intent.check();
    if (!current ||
        intent.generation != generation ||
        intent.conversationId != conversationId) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }

  Future<List<Message>> _readMergeSources(ChatBatchIntent intent) async {
    _checkBatchIntent(intent);
    final lease = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    final result = await _run((manager, access) {
      _checkBatchIntent(intent);
      return manager.messageManager.findMessageList(
        searchParams: [
          SearchParams(
            conversationID: conversationId,
            clientMsgIDList: intent.messageIds,
          ),
        ],
      );
    }, access: lease);
    lease.check();
    _checkBatchIntent(intent);
    final found = <String, Message>{};
    for (final group in [
      ...?result.findResultItems,
      ...?result.searchResultItems,
    ]) {
      if (group.conversationID != conversationId) continue;
      for (final message in group.messageList ?? <Message>[]) {
        final id = message.clientMsgID;
        if (!intent.messageIds.contains(id)) continue;
        if (id == null ||
            found.containsKey(id) ||
            isRemoved(id) ||
            message.status != MessageStatus.succeeded) {
          throw const AppFailure(FailureKind.dataChanged);
        }
        _raw[id] = message;
        final mapped = _map(message);
        if (!mapped.canForward || !canRetain(mapped)) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
        found[id] = message;
      }
    }
    if (found.length != intent.messageIds.length) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    _checkBatchIntent(intent);
    final ordered = found.values.toList()
      ..sort((a, b) {
        final order = (a.createTime ?? a.sendTime ?? 0).compareTo(
          b.createTime ?? b.sendTime ?? 0,
        );
        return order == 0 ? a.clientMsgID!.compareTo(b.clientMsgID!) : order;
      });
    return ordered;
  }

  void _checkMergeSources(ChatBatchIntent intent, Map<String, String> hashes) {
    _checkBatchIntent(intent);
    for (final id in intent.messageIds) {
      requireRetention(id);
      final raw = _raw[id];
      if (raw == null ||
          isRemoved(id) ||
          raw.status != MessageStatus.succeeded ||
          !_map(raw).canForward ||
          retainedMergeFingerprint(raw.toJson()) != hashes[id]) {
        throw const AppFailure(FailureKind.dataChanged);
      }
    }
  }

  /// Create one standard107 to one explicitly confirmed destination. Source
  /// protocol payloads live only in this bounded data operation, never in the
  /// selection, picker or confirmation dialog.
  Future<ChatMessage> forwardMergedSelection({
    required ChatRepository source,
    required ChatBatchIntent intent,
    void Function()? onDispatched,
  }) async {
    source._checkBatchIntent(intent);
    final destination = _conversation;
    if (destination == null ||
        !destination.canSend ||
        !current ||
        source.gateway != gateway ||
        source.generation != generation) {
      throw const AppFailure(FailureKind.cancelled);
    }
    final lease = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    final originals = await source._readMergeSources(intent);
    source._checkBatchIntent(intent);
    lease.check();
    final children = <Message>[];
    final hashes = <String, String>{};
    try {
      var total = 0;
      for (final raw in originals) {
        final clean = retainedMergeChild(raw.toJson());
        total += jsonEncode(clean).length;
        if (total > 2 * 1024 * 1024) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        children.add(Message.fromJson(clean));
        hashes[raw.clientMsgID!] = retainedMergeFingerprint(clean);
      }
      void checkSource() => source._checkMergeSources(intent, hashes);
      checkSource();
      lease.check();
      final created = await _run((manager, access) {
        checkSource();
        access.check();
        return manager.messageManager.createMergerMessage(
          messageList: children,
          title: '聊天记录',
          // Avoid body copies in stale summary strings or native notifications.
          summaryList: ['${children.length} 条消息'],
        );
      }, access: lease);
      checkSource();
      lease.check();
      final emitted = created.mergeElem?.multiMessage;
      if (created.contentType != MessageType.merger ||
          (created.clientMsgID ?? '').isEmpty ||
          hashes.containsKey(created.clientMsgID) ||
          (created.atTextElem?.atUserList?.isNotEmpty ?? false) ||
          emitted == null ||
          emitted.length != children.length) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      for (var i = 0; i < children.length; i++) {
        if (emitted[i].clientMsgID != children[i].clientMsgID ||
            retainedMergeFingerprint(emitted[i].toJson()) !=
                hashes[children[i].clientMsgID]) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
      }
      await source._readMergeSources(intent);
      checkSource();
      lease.check();
      final actual = await conversation();
      checkSource();
      lease.check();
      if (!actual.canSend ||
          actual.isGroup != destination.isGroup ||
          actual.userId != destination.userId ||
          actual.groupId != destination.groupId) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      _raw[created.clientMsgID!] = created;
      final sent = await send(
        created.clientMsgID!,
        access: lease,
        checkSource: checkSource,
        onDispatched: onDispatched,
      );
      checkSource();
      lease.check();
      return sent;
    } finally {
      originals.clear();
      children.clear();
      hashes.clear();
    }
  }

  Future<List<ChatCallCandidate>> callCandidates({int offset = 0}) async {
    final conversation = _conversation;
    if (conversation == null ||
        !conversation.isGroup ||
        conversation.groupId == null) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return _run((manager, access) async {
      final members = await manager.groupManager.getGroupMemberList(
        groupID: conversation.groupId!,
        offset: offset,
        count: 100,
      );
      return members
          .where(
            (member) =>
                (member.userID ?? '').isNotEmpty && member.userID != _userId,
          )
          .map(
            (member) => ChatCallCandidate(
              member.userID!,
              member.nickname ?? member.userID!,
            ),
          )
          .toList();
    });
  }

  Future<void> saveDraft(String text, {MessageHistoryAccess? access}) =>
      _run((manager, access) async {
        await manager.conversationManager.setConversationDraft(
          conversationID: conversationId,
          draftText: text,
        );
      }, access: access);

  Future<void> revoke(String id) => _run((manager, access) async {
    if (_raw[id]?.sendID != _userId) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await manager.messageManager.revokeMessage(
      conversationID: conversationId,
      clientMsgID: id,
    );
    forgetMessages([id]);
  });

  /// A native exception/timeout after onDispatched leaves an unknown result.
  /// Never fall back to local-only deletion or replay automatically.
  Future<void> deleteSelected(
    String id, {
    required void Function() checkSource,
    required void Function() onDispatched,
    required void Function() onAcknowledged,
  }) async {
    checkSource();
    final lease = gateway.historyAccess(
      generation,
      conversationId: conversationId,
    );
    final source = await findSource(id);
    checkSource();
    lease.check();
    if (source == null || !canView(source) || !canRetain(source)) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    await _run((manager, access) async {
      checkSource();
      access.check();
      if (isRemoved(id)) throw const AppFailure(FailureKind.dataChanged);
      onDispatched();
      await manager.messageManager.deleteMessageFromLocalAndSvr(
        conversationID: conversationId,
        clientMsgID: id,
      );
      onAcknowledged();
      checkSource();
      access.check();
      forgetMessages([id]);
    }, access: lease);
    checkSource();
    lease.check();
  }

  Future<void> delete(String id) => _run((manager, access) async {
    await manager.messageManager.deleteMessageFromLocalAndSvr(
      conversationID: conversationId,
      clientMsgID: id,
    );
    forgetMessages([id]);
  });

  Future<void> clearHistory() async {
    if (!current) throw const AppFailure(FailureKind.cancelled);
    await gateway.clearConversationMessages(generation, conversationId);
    resetHistory();
  }

  void clear() {
    _groupReadMarked.clear();
    _visibleReadQueue.clear();
    _raw.clear();
    _preparedFaces.clear();
    _preparedLocations.clear();
    _preparedCards.clear();
    _preparedQuoteSources.clear();
    _staged.clear();
    _conversation = null;
  }
}
