import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:path_provider/path_provider.dart';

import '../error/app_failure.dart';
import '../privacy/message_privacy.dart';
import '../tenant/session_coordinator.dart';
import '../tenant/session_credentials.dart';
import 'im_event.dart';

/// An immutable lease for a single history operation and its asynchronous
/// stages. A completed destructive request never renews an older lease.
final class MessageHistoryAccess {
  const MessageHistoryAccess._(
    this.revision,
    this._check, [
    this.conversationId,
  ]);
  final String? conversationId;
  final int revision;
  final void Function() _check;
  void check() => _check();
}

/// Owns the single native OpenIM session. Constructing this object does not
/// touch OpenIM, platform channels, application directories, or the network.
final class OpenImGateway implements ImGateway {
  OpenImGateway({
    required this.hasPrivacyConsent,
    this.onSessionExpired,
    IMManager Function()? managerFactory,
    Future<Directory> Function()? supportDirectory,
    this.operationTimeout = const Duration(seconds: 15),
    this.platformId,
    int Function()? interactiveRevision,
    bool Function()? hasInteractiveAccess,
  }) : _managerFactory = managerFactory ?? (() => OpenIM.iMManager),
       _supportDirectory = supportDirectory ?? getApplicationSupportDirectory,
       _interactiveRevision = interactiveRevision ?? (() => 0),
       _hasInteractiveAccess = hasInteractiveAccess ?? (() => true);
  final int Function() _interactiveRevision;
  final bool Function() _hasInteractiveAccess;

  final bool Function() hasPrivacyConsent;
  final IMManager Function() _managerFactory;
  final Future<Directory> Function() _supportDirectory;
  final int? platformId;
  final privacyLedger = MessagePrivacyLedger();
  final Duration operationTimeout;
  final void Function(int generation)? onSessionExpired;
  final _events = StreamController<ImEvent>.broadcast();
  final Set<Future<dynamic>> _pendingNativeCalls = {};
  Future<void> _tail = Future.value();
  Future<void>? _disposeFuture;
  IMManager? _manager;
  Directory? _dataDirectory;
  int _request = 0;
  int? _generation;
  bool _initialized = false;
  bool _loginAttempted = false;
  bool _quarantined = false;
  bool _sessionExpired = false;
  bool _disposed = false;
  int _historyRevision = 0;
  bool _clearingMessages = false;
  Future<void>? _clearMessagesOperation;
  int? _clearMessagesRequest;
  int? _clearMessagesGeneration;

  final Map<String, int> _conversationHistoryRevisions = {};
  final Map<String, Set<Future<dynamic>>> _conversationPending = {};
  final Set<String> _conversationQuarantined = {};
  final Map<
    String,
    ({
      int request,
      int generation,
      Object token,
      bool removeConversation,
      Future<void> future,
    })
  >
  _conversationClears = {};
  int conversationHistoryRevision(String id) =>
      _conversationHistoryRevisions[id] ?? 0;
  Map<String, int> get conversationHistorySnapshot =>
      Map.unmodifiable(_conversationHistoryRevisions);
  bool isConversationHistoryClearing(String id) =>
      _conversationClears.containsKey(id);

  int get historyRevision => _historyRevision;
  bool get isClearingMessages => _clearingMessages;

  void _requireSession(int request, int generation) {
    _check(request);
    if (_generation != generation ||
        !_initialized ||
        _manager?.isLogined != true ||
        _quarantined) {
      throw const AppFailure(FailureKind.sessionExpired);
    }
  }

  MessageHistoryAccess historyAccess(int generation, {String? conversationId}) {
    final request = _request;
    final revision = _historyRevision;
    final conversationRevision = conversationId == null
        ? null
        : conversationHistoryRevision(conversationId);
    final interactive = _interactiveRevision();
    void check() {
      _requireSession(request, generation);
      if (_clearingMessages ||
          revision != _historyRevision ||
          conversationId != null &&
              (isConversationHistoryClearing(conversationId) ||
                  _conversationQuarantined.contains(conversationId) ||
                  conversationRevision !=
                      conversationHistoryRevision(conversationId))) {
        throw const AppFailure(FailureKind.cancelled);
      }
      if (!_hasInteractiveAccess() || interactive != _interactiveRevision()) {
        throw const AppFailure(FailureKind.cancelled);
      }
    }

    check();
    return MessageHistoryAccess._(revision, check, conversationId);
  }

  Future<T> runForHistory<T>(
    int generation,
    Future<T> Function(IMManager manager, MessageHistoryAccess access)
    operation, {
    MessageHistoryAccess? access,
    String? conversationId,
  }) async {
    final scope = conversationId ?? access?.conversationId;
    final lease = access ?? historyAccess(generation, conversationId: scope);
    if (scope != lease.conversationId) {
      throw const AppFailure(FailureKind.cancelled);
    }
    lease.check();
    final result = runForSession(generation, (manager) async {
      lease.check();
      final value = await operation(manager, lease);
      lease.check();
      return value;
    }, historyConversationId: scope);
    return result;
  }

  /// A shared CID barrier across independently mounted chat/search repositories.
  /// Already dispatched native operations settle before clear; no Dart claim of
  /// native cancellation or proof of physical SQLite deletion is made.
  Future<void> clearConversationMessages(
    int generation,
    String conversationId, {
    bool removeConversation = false,
  }) {
    final request = _request;
    final interactive = _interactiveRevision();
    final globalRevision = _historyRevision;
    void check() {
      _requireSession(request, generation);
      if (_clearingMessages ||
          globalRevision != _historyRevision ||
          !_hasInteractiveAccess() ||
          interactive != _interactiveRevision()) {
        throw const AppFailure(FailureKind.cancelled);
      }
    }

    try {
      check();
      if (conversationId.isEmpty || conversationId.length > 512) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      final existing = _conversationClears[conversationId];
      if (existing != null) {
        return existing.request == request &&
                existing.generation == generation &&
                existing.removeConversation == removeConversation
            ? existing.future
            : Future.error(const AppFailure(FailureKind.cancelled));
      }
      if (_conversationQuarantined.contains(conversationId)) {
        throw const AppFailure(FailureKind.timeout);
      }
    } catch (error) {
      return Future.error(error);
    }
    final pending = List<Future<dynamic>>.of(
      _conversationPending[conversationId] ?? {},
    );
    final token = Object(), result = Completer<void>();
    _conversationClears[conversationId] = (
      request: request,
      generation: generation,
      token: token,
      removeConversation: removeConversation,
      future: result.future,
    );
    _conversationHistoryRevisions[conversationId] =
        conversationHistoryRevision(conversationId) + 1;
    privacyLedger.hideKnown(conversationId: conversationId);
    void publish({AppFailure? failure}) => _emit(
      request,
      generation,
      ImEventKind.conversationHistoryInvalidated,
      ids: [conversationId],
      failure: failure,
      historyClearing: isConversationHistoryClearing(conversationId),
    );
    publish();
    Future<void> clear() async {
      AppFailure? failure;
      try {
        try {
          await Future.wait(
            pending.map(
              (future) => future.then<void>(
                (_) {},
                onError: (Object _, StackTrace _) {},
              ),
            ),
          ).timeout(operationTimeout);
        } on TimeoutException {
          _conversationQuarantined.add(conversationId);
          throw const AppFailure(FailureKind.timeout);
        }
        check();
        await _native(
          () => removeConversation
              ? _manager!.conversationManager.deleteConversationAndDeleteAllMsg(
                  conversationID: conversationId,
                )
              : _manager!.conversationManager.clearConversationAndDeleteAllMsg(
                  conversationID: conversationId,
                ),
        );
        _requireSession(request, generation);
      } catch (error) {
        failure = _failure(error);
        throw failure;
      } finally {
        if (identical(_conversationClears[conversationId]?.token, token)) {
          _conversationClears.remove(conversationId);
        }
        publish(failure: failure);
      }
    }

    unawaited(
      clear().then<void>(
        (_) => result.complete(),
        onError: (Object error, StackTrace stack) =>
            result.completeError(error, stack),
      ),
    );
    return result.future;
  }

  /// One explicit request; duplicate callers join it. The ACK only confirms
  /// the native API response, not physical erasure in every old native core.
  Future<void> clearAllMessages(int generation) {
    final request = _request;
    final interactive = _interactiveRevision();
    void checkInteractive() {
      if (!_hasInteractiveAccess() || interactive != _interactiveRevision()) {
        throw const AppFailure(FailureKind.cancelled);
      }
    }

    try {
      _requireSession(request, generation);
      checkInteractive();
    } catch (error) {
      return Future.error(error);
    }
    final current = _clearMessagesOperation;
    if (current != null) {
      return _clearMessagesRequest == request &&
              _clearMessagesGeneration == generation
          ? current
          : Future.error(const AppFailure(FailureKind.cancelled));
    }
    final pending = List<Future<dynamic>>.of(_pendingNativeCalls);
    _clearingMessages = true;
    _clearMessagesRequest = request;
    _clearMessagesGeneration = generation;
    ++_historyRevision;
    privacyLedger.hideKnown();
    void publish({AppFailure? failure}) {
      _emit(
        request,
        generation,
        ImEventKind.historyInvalidated,
        failure: failure,
        historyRevision: _historyRevision,
        historyClearing: _clearingMessages,
      );
    }

    publish();
    Future<void> clear() async {
      AppFailure? failure;
      try {
        // Native operations already dispatched cannot be cancelled by Dart.
        // Freeze new history work, and wait for those operations to settle
        // before issuing the destructive request, with an explicit bound.
        try {
          await Future.wait(
            pending.map(
              (future) => future.then<void>(
                (_) {},
                onError: (Object _, StackTrace _) {},
              ),
            ),
          ).timeout(operationTimeout);
        } on TimeoutException {
          _quarantined = true;
          throw const AppFailure(FailureKind.timeout);
        }
        _requireSession(request, generation);
        checkInteractive();
        await _native(
          () => _manager!.messageManager.deleteAllMsgFromLocalAndSvr(),
        );
        _requireSession(request, generation);
      } catch (error) {
        failure = _failure(error);
        throw failure;
      } finally {
        _clearingMessages = false;
        _clearMessagesOperation = null;
        _clearMessagesRequest = null;
        _clearMessagesGeneration = null;
        publish(failure: failure);
      }
    }

    return _clearMessagesOperation = clear();
  }

  Stream<ImEvent> get events => _events.stream;

  Future<Directory> mediaDirectory(int generation) =>
      runForSession(generation, (_) async {
        final base = _dataDirectory;
        if (base == null) throw const AppFailure(FailureKind.storage);
        final directory = Directory('${base.path}/media');
        await directory.create(recursive: true).timeout(operationTimeout);
        return directory;
      });

  /// Data-layer boundary for message operations. Lifecycle methods remain
  /// owned by this gateway; callers map SDK objects into domain objects here.
  /// Results from a superseded account/line are discarded before delivery.
  Future<T> runForSession<T>(
    int generation,
    Future<T> Function(IMManager manager) operation, {
    bool allowWhileLocked = false,
    String? historyConversationId,
  }) async {
    final manager = _manager;
    final request = _request;
    final interactive = _interactiveRevision();
    void check() {
      _check(request);
      if (!allowWhileLocked &&
          (!_hasInteractiveAccess() || interactive != _interactiveRevision())) {
        throw const AppFailure(FailureKind.cancelled);
      }
      if (_generation != generation ||
          !_initialized ||
          manager == null ||
          !manager.isLogined ||
          _quarantined) {
        throw const AppFailure(FailureKind.sessionExpired);
      }
    }

    check();
    try {
      final value = await _native(
        () => operation(manager!),
        conversationId: historyConversationId,
      );
      check();
      return value;
    } catch (error) {
      check();
      throw _failure(error);
    }
  }

  Future<void> _serialize(Future<void> Function() action) {
    final result = Completer<void>();
    _tail = _tail.then((_) async {
      try {
        await action();
        result.complete();
      } catch (error, stack) {
        result.completeError(error, stack);
      }
    });
    return result.future;
  }

  void _check(int request) {
    if (_disposed || request != _request) {
      throw const AppFailure(FailureKind.cancelled);
    }
    if (_sessionExpired) throw const AppFailure(FailureKind.sessionExpired);
    if (!hasPrivacyConsent()) {
      throw const AppFailure(FailureKind.configuration);
    }
  }

  Future<T> _native<T>(
    Future<T> Function() operation, {
    String? conversationId,
  }) {
    final future = Future<T>.sync(operation);
    _pendingNativeCalls.add(future);
    final scoped = conversationId == null
        ? null
        : _conversationPending.putIfAbsent(conversationId, () => {});
    scoped?.add(future);
    void remove() {
      _pendingNativeCalls.remove(future);
      scoped?.remove(future);
      if (scoped?.isEmpty == true &&
          identical(_conversationPending[conversationId], scoped)) {
        _conversationPending.remove(conversationId);
      }
    }

    unawaited(
      future.then<void>(
        (_) {
          remove();
        },
        onError: (Object _, StackTrace _) {
          remove();
        },
      ),
    );
    return future.timeout(
      operationTimeout,
      onTimeout: () {
        // A Dart timeout cannot cancel a native login. Block another login while
        // an old native call may still mutate the process-global SDK session.
        _quarantined = true;
        throw const AppFailure(FailureKind.timeout);
      },
    );
  }

  AppFailure _failure(Object error) {
    if (error is AppFailure) return error;
    if (error is TimeoutException) return const AppFailure(FailureKind.timeout);
    if (error is FileSystemException) {
      return const AppFailure(FailureKind.storage);
    }
    if (error is MissingPluginException) {
      return const AppFailure(FailureKind.configuration);
    }
    if (error is PlatformException) {
      final code = int.tryParse(error.code);
      if (code == 10005) {
        // This generic native code also covers non-network SDK failures. Only
        // fixed transport markers may refine it; raw native text never leaves
        // this boundary, even when it contains a URL, token or message body.
        final text = (error.message ?? '').toLowerCase();
        if (text.contains('x509:') ||
            text.contains('tls: failed to verify certificate')) {
          return AppFailure(FailureKind.configuration, code: code);
        }
        if (text.contains('i/o timeout') ||
            text.contains('context deadline exceeded') ||
            text.contains('tls handshake timeout') ||
            text.contains('client.timeout exceeded') ||
            text.contains('connection timed out')) {
          return AppFailure(FailureKind.timeout, code: code);
        }
        if (text.contains('no such host') ||
            text.contains('temporary failure in name resolution') ||
            text.contains('connection refused') ||
            text.contains('network is unreachable') ||
            text.contains('no route to host')) {
          return AppFailure(FailureKind.network, code: code);
        }
      }
      if (code != null) return AppFailure.fromCode(code);
    }
    return const AppFailure(FailureKind.unknown);
  }

  @override
  Future<void> connect(SessionCredentials credentials, int generation) {
    if (_disposed) return Future.error(const AppFailure(FailureKind.cancelled));
    if (!hasPrivacyConsent()) {
      return Future.error(const AppFailure(FailureKind.configuration));
    }
    final request = ++_request;
    _sessionExpired = false;
    _generation = null;
    privacyLedger.endSession();
    return _serialize(() async {
      _check(request);
      try {
        if (_quarantined && _pendingNativeCalls.isNotEmpty) {
          throw const AppFailure(FailureKind.timeout);
        }
        await _cleanup(force: _quarantined);
        _check(request);
        _quarantined = false;
        _conversationHistoryRevisions.clear();
        _conversationPending.clear();
        _conversationQuarantined.clear();
        _conversationClears.clear();
        privacyLedger.bind(credentials.accountKey, generation);
        final platform =
            platformId ??
            (Platform.isIOS
                ? 1
                : Platform.isAndroid
                ? 2
                : 0);
        if (platform == 0) throw const AppFailure(FailureKind.configuration);
        final support = await _supportDirectory().timeout(operationTimeout);
        _check(request);
        final directory = Directory(
          '${support.path}/openim/${credentials.accountKey}',
        );
        await directory.create(recursive: true).timeout(operationTimeout);
        _check(request);
        final manager = _manager ??= _managerFactory();
        _dataDirectory = directory;
        _generation = generation;
        _initialized = true;
        final initialized = await _native(
          () => manager.initSDK(
            platformID: platform,
            apiAddr: credentials.endpoints.im.toString(),
            wsAddr: credentials.endpoints.socket.toString(),
            dataDir: directory.path,
            logFilePath: '${directory.path}/logs',
            logLevel: 1,
            isLogStandardOutput: false,
            listener: _connectionListener(request, generation),
          ),
        );
        _check(request);
        if (initialized != true) {
          throw const AppFailure(FailureKind.configuration);
        }
        await _registerListeners(manager, request, generation);
        _check(request);
        _loginAttempted = true;
        final profile = await _native(
          () => manager.login(
            userID: credentials.userId,
            token: credentials.imToken,
            checkLoginStatus: false,
          ),
        );
        _check(request);
        if (profile.userID != credentials.userId) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
      } catch (error) {
        _generation = null;
        try {
          await _cleanup();
        } catch (_) {
          _quarantined = true;
        }
        throw _failure(error);
      }
    });
  }

  @override
  Future<void> disconnect() {
    if (_disposed) return _disposeFuture ?? Future.value();
    ++_request;
    _generation = null;
    privacyLedger.endSession();
    return _serialize(_cleanup);
  }

  Future<void> _cleanup({bool force = false}) async {
    _generation = null;
    // Uploads and other data operations can outlive a cancelled Dart request.
    // Do not let a subsequent account reuse this process-global SDK until they
    // have settled and a fresh unInit barrier has run.
    if (_pendingNativeCalls.isNotEmpty) _quarantined = true;
    final manager = _manager;
    if (manager == null || (!_initialized && !force)) return;
    if (_loginAttempted && !_quarantined) {
      try {
        await _native(() => manager.logout());
      } catch (_) {
        /* unInitSDK is the authoritative local-session teardown. */
      }
    }
    _loginAttempted = false;
    try {
      await _native(() => manager.unInitSDK());
      _initialized = false;
      _dataDirectory = null;
    } catch (error) {
      _quarantined = true;
      throw _failure(error);
    }
  }

  void _emit(
    int request,
    int generation,
    ImEventKind kind, {
    Iterable<String?> ids = const [],
    int? count,
    AppFailure? failure,
    ImSignalAction? signal,
    ImCallInvitation? call,
    ImTypingStatus? typing,
    int? historyRevision,
    bool? historyClearing,
  }) {
    if (_disposed || request != _request || generation != _generation) return;
    _events.add(
      ImEvent(
        kind: kind,
        generation: generation,
        resourceIds: ids.whereType<String>().where((id) => id.isNotEmpty),
        count: count,
        failure: failure,
        signalAction: signal,
        call: call,
        typing: typing,
        historyRevision: historyRevision,
        historyClearing: historyClearing,
      ),
    );
  }

  OnConnectListener _connectionListener(int request, int generation) {
    void expire(ImEventKind kind) {
      if (request != _request || generation != _generation || _disposed) return;
      _emit(
        request,
        generation,
        kind,
        failure: const AppFailure(FailureKind.sessionExpired),
      );
      _generation = null;
      _sessionExpired = true;
      try {
        onSessionExpired?.call(generation);
      } catch (_) {
        /* No raw error logging. */
      }
    }

    return OnConnectListener(
      onConnecting: () => _emit(request, generation, ImEventKind.connecting),
      onConnectSuccess: () => _emit(request, generation, ImEventKind.connected),
      onConnectFailed: (code, message) => _emit(
        request,
        generation,
        ImEventKind.connectionFailed,
        failure: code == null
            ? const AppFailure(FailureKind.network)
            : _failure(PlatformException(code: '$code', message: message)),
      ),
      onKickedOffline: () => expire(ImEventKind.kickedOffline),
      onUserTokenExpired: () => expire(ImEventKind.tokenExpired),
    );
  }

  Future<void> _registerListeners(
    IMManager manager,
    int request,
    int generation,
  ) async {
    void emit(
      ImEventKind kind, {
      Iterable<String?> ids = const [],
      int? count,
      ImSignalAction? signal,
      ImCallInvitation? call,
      ImTypingStatus? typing,
    }) => _emit(
      request,
      generation,
      kind,
      ids: ids,
      count: count,
      signal: signal,
      call: call,
      typing: typing,
    );
    Future<void> register(Future<dynamic> Function() action) async {
      _check(request);
      await _native(action);
      _check(request);
    }

    bool active() =>
        !_disposed && request == _request && generation == _generation;
    String messageConversation(Message item) {
      final group = item.groupID ?? '';
      if (item.sessionType == 2 && group.isNotEmpty) return 'g_$group';
      if (item.sessionType == 3 && group.isNotEmpty) return 'sg_$group';
      final sender = item.sendID ?? '', receiver = item.recvID ?? '';
      if (sender.isEmpty || receiver.isEmpty) return '';
      if (item.sessionType == 1) {
        final pair = [sender, receiver]..sort();
        return 'si_${pair.join('_')}';
      }
      if (item.sessionType == 4) return 'sn_${sender}_$receiver';
      return '';
    }

    bool consumeTyping(Message item, {bool offline = false}) {
      if (item.contentType != MessageType.typing) return false;
      // Malformed/offline typing is still transient and must never enter the
      // privacy ledger or ordinary-message refresh path.
      final sender = item.sendID ?? '', recipient = item.recvID ?? '';
      bool identity(String value) =>
          value.isNotEmpty &&
          value.length <= 256 &&
          !RegExp(r'[\s\x00-\x1f\x7f-\x9f]').hasMatch(value);
      final tip = item.typingElem?.msgTips;
      if (!offline &&
          active() &&
          item.sessionType == 1 &&
          (item.groupID ?? '').isEmpty &&
          identity(sender) &&
          identity(recipient) &&
          sender != recipient &&
          const {'yes', 'no'}.contains(tip)) {
        emit(
          ImEventKind.typingChanged,
          typing: ImTypingStatus(sender, recipient, tip == 'yes'),
        );
      }
      return true;
    }

    void rememberMessage(Message item, [String conversationId = '']) {
      if (!active() || item.contentType == MessageType.typing) return;
      final id = conversationId.isEmpty
          ? messageConversation(item)
          : conversationId;
      if (_clearingMessages || isConversationHistoryClearing(id)) {
        privacyLedger.forget([item.clientMsgID ?? '']);
      } else {
        privacyLedger.observe(id, item.toJson(), now: DateTime.now());
      }
    }

    void rememberConversations(List<ConversationInfo> items) {
      for (final item in items) {
        final latest = item.latestMsg;
        if (latest != null) rememberMessage(latest, item.conversationID);
      }
    }

    await register(
      () => manager.conversationManager.setConversationListener(
        OnConversationListener(
          onSyncServerStart: () => emit(ImEventKind.syncStarted),
          onSyncServerFinish: () => emit(ImEventKind.syncFinished),
          onSyncServerFailed: () => emit(ImEventKind.syncFailed),
          onNewConversation: (items) {
            rememberConversations(items);
            emit(
              ImEventKind.conversationsChanged,
              ids: items.map((item) => item.conversationID),
            );
          },
          onConversationChanged: (items) {
            rememberConversations(items);
            emit(
              ImEventKind.conversationsChanged,
              ids: items.map((item) => item.conversationID),
            );
          },
          onTotalUnreadMessageCountChanged: (count) =>
              emit(ImEventKind.unreadChanged, count: count),
        ),
      ),
    );
    await register(
      () => manager.messageManager.setAdvancedMsgListener(
        OnAdvancedMsgListener(
          onRecvNewMessage: (item) {
            if (consumeTyping(item)) return;
            rememberMessage(item);
            emit(ImEventKind.messageReceived, ids: [item.clientMsgID]);
          },
          onRecvOfflineNewMessage: (item) {
            if (consumeTyping(item, offline: true)) return;
            rememberMessage(item);
            emit(ImEventKind.offlineMessageReceived, ids: [item.clientMsgID]);
          },
          onRecvOnlineOnlyMessage: (item) {
            if (consumeTyping(item)) return;
            emit(ImEventKind.transientMessageReceived, ids: [item.clientMsgID]);
          },
          onMsgDeleted: (item) {
            if (active()) privacyLedger.forget([item.clientMsgID ?? '']);
            emit(ImEventKind.messageDeleted, ids: [item.clientMsgID]);
          },
          onNewRecvMessageRevoked: (item) {
            if (active()) privacyLedger.forget([item.clientMsgID ?? '']);
            emit(ImEventKind.messageRevoked, ids: [item.clientMsgID]);
          },
          onRecvC2CReadReceipt: (items) => emit(
            ImEventKind.readReceiptChanged,
            ids: items.expand((item) => item.msgIDList ?? <String>[]),
          ),
          onRecvGroupReadReceipt: (item) =>
              emit(ImEventKind.readReceiptChanged, ids: [item.conversationID]),
          onRecvMessageExtensionsAdded: (id, _) =>
              emit(ImEventKind.messageExtensionsChanged, ids: [id]),
          onRecvMessageExtensionsChanged: (id, _) =>
              emit(ImEventKind.messageExtensionsChanged, ids: [id]),
          onRecvMessageExtensionsDeleted: (id, _) =>
              emit(ImEventKind.messageExtensionsChanged, ids: [id]),
        ),
      ),
    );
    manager.messageManager.setMsgSendProgressListener(
      OnMsgSendProgressListener(
        onProgress: (id, progress) => emit(
          ImEventKind.sendProgress,
          ids: [id],
          count: progress.clamp(0, 100),
        ),
      ),
    );
    await register(
      () => manager.groupManager.setGroupListener(
        OnGroupListener(
          onGroupApplicationAccepted: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupApplicationAdded: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupApplicationDeleted: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupApplicationRejected: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupDismissed: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupInfoChanged: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupMemberAdded: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupMemberDeleted: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onGroupMemberInfoChanged: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onJoinedGroupAdded: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
          onJoinedGroupDeleted: (item) =>
              emit(ImEventKind.groupsChanged, ids: [item.groupID]),
        ),
      ),
    );
    await register(
      () => manager.friendshipManager.setFriendshipListener(
        OnFriendshipListener(
          onBlackAdded: (item) =>
              emit(ImEventKind.contactsChanged, ids: [item.blockUserID]),
          onBlackDeleted: (item) =>
              emit(ImEventKind.contactsChanged, ids: [item.blockUserID]),
          onFriendAdded: (item) =>
              emit(ImEventKind.contactsChanged, ids: [item.userID]),
          onFriendDeleted: (item) =>
              emit(ImEventKind.contactsChanged, ids: [item.userID]),
          onFriendInfoChanged: (item) =>
              emit(ImEventKind.contactsChanged, ids: [item.userID]),
          onFriendApplicationAccepted: (item) => emit(
            ImEventKind.contactsChanged,
            ids: [item.fromUserID, item.toUserID],
          ),
          onFriendApplicationAdded: (item) => emit(
            ImEventKind.contactsChanged,
            ids: [item.fromUserID, item.toUserID],
          ),
          onFriendApplicationDeleted: (item) => emit(
            ImEventKind.contactsChanged,
            ids: [item.fromUserID, item.toUserID],
          ),
          onFriendApplicationRejected: (item) => emit(
            ImEventKind.contactsChanged,
            ids: [item.fromUserID, item.toUserID],
          ),
        ),
      ),
    );
    await register(
      () => manager.userManager.setUserListener(
        OnUserListener(
          onSelfInfoUpdated: (item) =>
              emit(ImEventKind.selfProfileChanged, ids: [item.userID]),
          onUserStatusChanged: (item) =>
              emit(ImEventKind.presenceChanged, ids: [item.userID]),
        ),
      ),
    );
    await register(
      () => manager.messageManager.setCustomBusinessListener(
        OnCustomBusinessListener(
          onRecvCustomBusinessMessage: (_) =>
              emit(ImEventKind.customBusinessReceived),
        ),
      ),
    );
    await register(
      () => manager.messageManager.setMessageKvInfoListener(
        OnMessageKvInfoListener(
          onMessageKvInfoChanged: (items) => emit(
            ImEventKind.messageExtensionsChanged,
            ids: items.map((item) => item.clientMsgID),
          ),
        ),
      ),
    );
    void signal(ImSignalAction action, SignalingInfo item) {
      final invitation = item.invitation;
      final roomId = invitation?.roomID ?? '';
      emit(
        ImEventKind.signalingChanged,
        signal: action,
        ids: [roomId],
        call: invitation == null || roomId.isEmpty
            ? null
            : ImCallInvitation(
                roomId: roomId,
                inviterId: invitation.inviterUserID ?? '',
                inviteeIds: invitation.inviteeUserIDList ?? const [],
                groupId: invitation.groupID ?? '',
                actorId: item.userID ?? '',
                mediaType: invitation.mediaType == 'video' ? 'video' : 'audio',
                timeoutSeconds: (invitation.timeout ?? 30).clamp(1, 120),
                initiatedAt: DateTime.fromMillisecondsSinceEpoch(
                  (invitation.initiateTime ?? 0) * 1000,
                  isUtc: true,
                ),
                originPlatform: invitation.platformID ?? 0,
                sessionType: invitation.sessionType ?? 1,
              ),
      );
    }

    await register(
      () => manager.signalingManager.setSignalingListener(
        OnSignalingListener(
          onReceiveNewInvitation: (item) =>
              signal(ImSignalAction.invited, item),
          onInvitationCancelled: (item) =>
              signal(ImSignalAction.cancelled, item),
          onInvitationTimeout: (item) => signal(ImSignalAction.timedOut, item),
          onInviteeAccepted: (item) => signal(ImSignalAction.accepted, item),
          onInviteeRejected: (item) => signal(ImSignalAction.rejected, item),
          onInviteeAcceptedByOtherDevice: (item) =>
              signal(ImSignalAction.acceptedOnOtherDevice, item),
          onInviteeRejectedByOtherDevice: (item) =>
              signal(ImSignalAction.rejectedOnOtherDevice, item),
          onHangup: (item) => signal(ImSignalAction.hungUp, item),
          onRoomParticipantConnected: (_) => emit(
            ImEventKind.signalingChanged,
            signal: ImSignalAction.participantConnected,
          ),
          onRoomParticipantDisconnected: (_) => emit(
            ImEventKind.signalingChanged,
            signal: ImSignalAction.participantDisconnected,
          ),
          onMeetingStreamChanged: (_) => emit(
            ImEventKind.signalingChanged,
            signal: ImSignalAction.meetingStreamChanged,
          ),
          onReceiveCustomSignal: (_) => emit(
            ImEventKind.signalingChanged,
            signal: ImSignalAction.customSignal,
          ),
        ),
      ),
    );
  }

  Future<void> dispose() => _disposeFuture ??= _dispose();

  Future<void> _dispose() async {
    _disposed = true;
    ++_request;
    _generation = null;
    try {
      await _serialize(_cleanup);
    } finally {
      await privacyLedger.dispose();
      await _events.close();
    }
  }
}
