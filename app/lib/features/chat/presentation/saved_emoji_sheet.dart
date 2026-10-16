import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/storage/object_uploader.dart';
import '../../../core/storage/staged_image.dart';
import '../../../core/tenant/session_access.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../data/saved_emoji_store.dart';
import '../domain/saved_emoji.dart';
import 'chat_controller.dart';

final savedEmojiPickerProvider = Provider<Future<List<XFile>> Function()>(
  (_) =>
      () => ImagePicker().pickMultiImage(limit: 9, requestFullMetadata: false),
);
final savedEmojiUploaderProvider = Provider<ObjectUploader Function(TenantApi)>(
  (_) =>
      (api) => ObjectUploader(api),
);
final savedEmojiImageProvider = Provider<ImageProvider<Object> Function(Uri)>(
  (_) =>
      (uri) => NetworkImage(uri.toString()),
);
const chatUnicodeEmoji = [
  '😀',
  '😊',
  '🥰',
  '😎',
  '😂',
  '😭',
  '👍',
  '🙏',
  '❤️',
  '🎉',
  '🌹',
  '🤝',
  '🤔',
  '😴',
  '👋',
  '🔥',
  '👏',
  '💪',
  '✨',
  '🥳',
];

class SavedEmojiSheet extends ConsumerStatefulWidget {
  const SavedEmojiSheet({
    super.key,
    required this.controller,
    required this.sourceCurrent,
    required this.insertUnicode,
    this.withOwnedPicker,
  });
  final ChatController controller;
  final bool Function() sourceCurrent;
  final void Function(String) insertUnicode;
  final Future<void> Function(Future<void> Function())? withOwnedPicker;
  @override
  ConsumerState<SavedEmojiSheet> createState() => _SavedEmojiSheetState();
}

class _SavedEmojiSheetState extends ConsumerState<SavedEmojiSheet> {
  late final SessionAccess _access;
  late final ChatController _controller;
  late final MessageHistoryAccess _cut;
  late final SavedEmojiStore _store;
  late final TenantApi? _api;
  ModalRoute<dynamic>? _route;
  StreamSubscription<SavedEmojiChange>? _changes;
  StreamSubscription<SessionSnapshot>? _sessions;
  StreamSubscription<int>? _invalidations, _accessChanges;
  StreamSubscription<void>? _privacy;
  StreamSubscription<ImEvent>? _events;
  final _proofs = <String, void Function()>{};
  final _removedSourceIds = <String>{};
  final _selected = <String>{};
  SavedEmojiSnapshot? _snapshot;
  StagedImage? _preview;
  final _remaining = <StagedImage>[];
  SavedEmoji? _receipt;
  String? _receiptNonce;
  ObjectUploader? _uploader;
  int _view = 0, _read = 0, _pick = 0;
  bool _retired = false,
      _loading = true,
      _busy = false,
      _managing = false,
      _redrawQueued = false;
  String? _error;
  bool _current(int view) {
    if (!mounted ||
        _retired ||
        view != _view ||
        !widget.sourceCurrent() ||
        !identical(widget.controller, _controller) ||
        !_controller.usable ||
        !identical(ref.read(tenantApiProvider), _api) ||
        _route?.isCurrent != true ||
        !identical(_access.session.state.credentials, _access.credentials) ||
        !_access.isCurrent) {
      return false;
    }
    try {
      _cut.check();
      return true;
    } on AppFailure {
      return false;
    }
  }

  void _check(int view) {
    if (!_current(view)) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }

  @override
  void initState() {
    super.initState();
    _controller = widget.controller;
    final repository = _controller.repository;
    _access = SessionAccess(repository.session);
    _cut = repository.gateway.historyAccess(
      repository.generation,
      conversationId: repository.conversationId,
    );
    _store = ref.read(savedEmojiStoreProvider);
    _api = ref.read(tenantApiProvider);
    _changes = _store.changes.listen((change) {
      if (change.accountKey == _access.credentials.accountKey &&
          !_busy &&
          _current(_view)) {
        unawaited(_load());
      }
    });
    void ownerChanged() {
      if (!_access.isCurrent ||
          !identical(_access.session.state.credentials, _access.credentials)) {
        _retire();
      }
    }

    _sessions = _access.session.changes.listen((_) => ownerChanged());
    _invalidations = _access.session.invalidations.listen((_) => _retire());
    _accessChanges = _access.session.accessChanges.listen(
      (_) => ownerChanged(),
    );
    _privacy = repository.gateway.privacyLedger.changes.listen((_) {
      if (_current(_view)) {
        _redraw();
      }
    });
    _events = repository.gateway.events.listen((event) {
      if (event.generation != repository.generation) {
        return;
      }
      if (event.kind == ImEventKind.messageDeleted ||
          event.kind == ImEventKind.messageRevoked) {
        _removedSourceIds.addAll(event.resourceIds);
        if (mounted) {
          _redraw();
        }
      }
      if (event.kind == ImEventKind.historyInvalidated ||
          event.kind == ImEventKind.conversationHistoryInvalidated) {
        if (mounted) {
          _redraw();
        } // Re-evaluate every original source-CID proof, never renew it.
      }
      if (event.kind == ImEventKind.conversationsChanged &&
          !_busy &&
          _current(_view)) {
        unawaited(_load());
      }
      try {
        _cut.check();
      } on AppFailure {
        _retire();
      }
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_current(_view)) {
        unawaited(_load());
      }
    });
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _route ??= ModalRoute.of(context);
    if (_route?.isCurrent != true) _retire();
  }

  void _redraw() {
    if (_redrawQueued || !mounted) {
      return;
    }
    _redrawQueued = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _redrawQueued = false;
      if (mounted) {
        setState(() {});
      }
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  void _retire() {
    if (_retired) {
      return;
    }
    _retired = true;
    ++_view;
    ++_pick;
    ++_read;
    _proofs.clear();
    _selected.clear();
    _uploader?.close();
    if (!_busy) {
      unawaited(_disposePreviews());
    }
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        setState(() {});
      }
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  bool _visible(SavedEmoji entry) {
    if (entry.source != null &&
        _removedSourceIds.contains(entry.source!.messageId)) {
      return false;
    }
    try {
      _proofs[entry.id]?.call();
      return _proofs.containsKey(entry.id);
    } on AppFailure {
      return false;
    }
  }

  Future<void> _load({bool preserveError = false}) async {
    final view = _view, read = ++_read;
    if (!_current(view)) {
      return;
    }
    setState(() {
      _loading = true;
      if (!preserveError) {
        _error = null;
      }
    });
    try {
      final next = await _store.read(_access, current: () => _current(view));
      _check(view);
      final proofs = <String, void Function()>{};
      for (final entry in next.entries) {
        try {
          final proof = await widget.controller.repository.savedEmojiProof(
            entry,
            access: _cut,
            checkSource: () => _check(view),
          );
          _check(view);
          proofs[entry.id] = proof;
        } on AppFailure catch (error) {
          if (error.kind != FailureKind.permissionDenied) {
            rethrow;
          }
        }
      }
      _check(view);
      if (read != _read) {
        return;
      }
      StagedImage? committed;
      if (_receipt != null &&
          next.pendingUpload == null &&
          next.entries.any((e) => e.id == _receipt!.id)) {
        committed = _preview;
        _receipt = null;
        _receiptNonce = null;
        _preview = _remaining.isEmpty ? null : _remaining.removeAt(0);
      }
      setState(() {
        _snapshot = next;
        _proofs
          ..clear()
          ..addAll(proofs);
        _loading = false;
      });
      await committed?.dispose();
    } catch (error) {
      if (_current(view) && read == _read) {
        setState(() {
          _loading = false;
          _error = error is AppFailure ? error.message : '常用表情读取失败，请重试';
        });
      }
    }
  }

  Future<void> _disposePreviews() async {
    final files = [?_preview, ..._remaining];
    _preview = null;
    _remaining.clear();
    for (final file in files) {
      await file.dispose();
    }
  }

  Future<void> _choose() async {
    final view = _view;
    if (!_current(view) || _busy || _snapshot?.pendingUpload != null) {
      return;
    }
    final pick = ++_pick;
    setState(() {
      _busy = true;
      _error = null;
    });
    final prepared = <StagedImage>[];
    try {
      late List<XFile> files;
      Future<void> invokePicker() async {
        files = await ref.read(savedEmojiPickerProvider)();
      }

      if (widget.withOwnedPicker case final ownedPicker?) {
        await ownedPicker(invokePicker);
      } else {
        await invokePicker();
      }
      _check(view);
      if (files.isEmpty) {
        return;
      }
      if (files.length > 9) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      for (final file in files) {
        _check(view);
        prepared.add(
          await ImageStager(
            widget.controller.repository.gateway,
            _access,
          ).prepare(file.path, allowGif: true),
        );
        _check(view);
      }
      if (pick != _pick) {
        return;
      }
      await _disposePreviews();
      _check(view);
      setState(() {
        _preview = prepared.removeAt(0);
        _remaining.addAll(prepared);
        prepared.clear();
        _receipt = null;
        _receiptNonce = null;
      });
    } catch (error) {
      if (_current(view)) {
        setState(() {
          _error = error is AppFailure ? error.message : '图片读取失败，请重试';
        });
      }
    } finally {
      for (final file in prepared) {
        await file.dispose();
      }
      if (!mounted || !_current(view)) await _disposePreviews();
      if (mounted) {
        setState(() => _busy = false);
      }
    }
  }

  Future<void> _saveGallery() async {
    final view = _view;
    if (!_current(view) ||
        _busy ||
        _snapshot == null ||
        _api == null ||
        !identical(_api.access.credentials, _access.credentials)) {
      return;
    }
    if (_receipt != null &&
        (_receiptNonce == null || _snapshot!.pendingUpload != _receiptNonce)) {
      setState(() => _error = '上传记录已变化，请重新核对后选择。');
      return;
    }
    if (_receipt == null &&
        (_preview == null || _snapshot!.pendingUpload != null)) {
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      while (_preview != null || _receipt != null) {
        _check(view);
        final staged = _preview;
        if (_receipt == null) {
          _snapshot = await _store.beginUpload(
            _access,
            _snapshot!,
            current: () => _current(view),
          );
          _receiptNonce = _snapshot!.pendingUpload;
          _check(view);
          final uploader = ref.read(savedEmojiUploaderProvider)(_api);
          _uploader = uploader;
          try {
            final url = await uploader.upload(
              staged!.file,
              contentType: staged.contentType,
              extension: staged.extension,
              group: 'saved-emoji',
              checkSource: () => _check(view),
            );
            _check(view);
            _receipt = SavedEmoji(
              url: url,
              width: staged.width,
              height: staged.height,
            );
          } finally {
            uploader.close();
            if (identical(_uploader, uploader)) _uploader = null;
          }
        }
        _check(view);
        final next = await _store.finishUpload(
          _access,
          _snapshot!,
          _receiptNonce!,
          _receipt!,
          current: () => _current(view),
        );
        _check(view);
        setState(() {
          _snapshot = next;
          _receipt = null;
          _receiptNonce = null;
          _preview = _remaining.isEmpty ? null : _remaining.removeAt(0);
        });
        await staged?.dispose();
      }
    } catch (_) {
      if (_current(view)) {
        if (_receipt == null && _snapshot?.pendingUpload != null) {
          // No final URL receipt: do not retry this image. Keep only remaining
          // unstarted selections for a later explicit decision after resolution.
          final uncertain = _preview;
          _preview = null;
          await uncertain?.dispose();
        }
        if (_current(view)) {
          setState(() {
            _error = _receipt != null
                ? '图片已上传，保存常用表情失败。可以重试保存。'
                : '上传结果未确认，请先核对。不会自动再次上传。';
          });
        }
      }
    } finally {
      // Already dispatched I/O is not claimed cancelled. Its Future has now
      // terminated; delete only owned staging inputs, never originals/assets.
      if (!mounted || !_current(view)) await _disposePreviews();
      if (mounted) {
        setState(() => _busy = false);
      }
      if (_current(view)) await _load(preserveError: true);
    }
  }

  Future<void> _remove() async {
    final view = _view;
    if (!_current(view) || _busy || _snapshot == null || _selected.isEmpty) {
      return;
    }
    setState(() => _busy = true);
    try {
      final next = await _store.remove(
        _access,
        _snapshot!,
        Set.of(_selected),
        current: () => _current(view),
      );
      _check(view);
      setState(() {
        _snapshot = next;
        _selected.clear();
      });
    } catch (error) {
      if (_current(view)) {
        setState(() {
          _error = error is AppFailure ? error.message : '删除失败，请重试';
        });
      }
    } finally {
      if (mounted) {
        setState(() => _busy = false);
      }
    }
  }

  Future<void> _endPending() async {
    final view = _view;
    if (!_current(view) || _busy || _snapshot == null || _receipt != null) {
      return;
    }
    setState(() => _busy = true);
    try {
      final next = await _store.endPending(
        _access,
        _snapshot!,
        current: () => _current(view),
      );
      _check(view);
      setState(() {
        _snapshot = next;
        _receipt = null;
        _receiptNonce = null;
        _error = null;
        if (_preview == null && _remaining.isNotEmpty) {
          _preview = _remaining.removeAt(0);
        }
      });
    } catch (error) {
      if (_current(view)) {
        setState(() {
          _error = error is AppFailure ? error.message : '更新失败，请重试';
        });
      }
    } finally {
      if (mounted) {
        setState(() => _busy = false);
      }
    }
  }

  Future<void> _send(SavedEmoji entry) async {
    final view = _view;
    if (!_current(view) || _busy || !_visible(entry)) {
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    final result = await widget.controller.sendSavedEmoji(
      entry,
      access: _cut,
      checkSource: () {
        _check(view);
        if (!_visible(entry)) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
      },
    );
    if (mounted) {
      setState(() => _busy = false);
    }
    if (!_current(view)) {
      return;
    }
    setState(() {
      _error = switch (result) {
        SavedEmojiSendOutcome.sent => '表情已发送',
        SavedEmojiSendOutcome.unconfirmed => '发送结果未确认，请在聊天中核对原消息。',
        SavedEmojiSendOutcome.notDispatched =>
          widget.controller.hasUnconfirmedFace(entry.id)
              ? '请先在聊天中核对上一次发送。'
              : widget.controller.failure?.message ?? '暂时无法发送，请重试',
      };
    });
  }

  @override
  void dispose() {
    _retired = true;
    ++_view;
    ++_read;
    ++_pick;
    _uploader?.close();
    if (!_busy) {
      unawaited(_disposePreviews());
    }
    unawaited(_changes?.cancel());
    unawaited(_sessions?.cancel());
    unawaited(_invalidations?.cancel());
    unawaited(_accessChanges?.cancel());
    unawaited(_privacy?.cancel());
    unawaited(_events?.cancel());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final api = ref.watch(tenantApiProvider);
    if (!identical(api, _api)) _retire();
    final valid = _current(_view) && identical(api, _api);
    if (!valid) {
      return const SafeArea(
        child: Padding(
          padding: EdgeInsets.all(24),
          child: Text('本次表情选择已失效，请关闭后重新打开。'),
        ),
      );
    }
    return SafeArea(
      child: SizedBox(
        height: MediaQuery.sizeOf(context).height * .72,
        child: DefaultTabController(
          length: 2,
          child: Column(
            children: [
              const TabBar(
                tabs: [
                  Tab(text: '表情'),
                  Tab(text: '常用表情'),
                ],
              ),
              Expanded(
                child: TabBarView(
                  children: [
                    SingleChildScrollView(
                      padding: const EdgeInsets.all(16),
                      child: Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        children: [
                          for (final emoji in chatUnicodeEmoji)
                            IconButton(
                              constraints: const BoxConstraints(
                                minWidth: 44,
                                minHeight: 44,
                              ),
                              visualDensity: VisualDensity.standard,
                              tooltip: emoji,
                              onPressed: () {
                                if (_current(_view)) {
                                  widget.insertUnicode(emoji);
                                }
                              },
                              icon: Text(
                                emoji,
                                style: const TextStyle(fontSize: 26),
                              ),
                            ),
                        ],
                      ),
                    ),
                    Column(
                      children: [
                        Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 12),
                          child: Wrap(
                            spacing: 8,
                            children: [
                              TextButton.icon(
                                onPressed:
                                    _busy ||
                                        _loading ||
                                        _snapshot?.pendingUpload != null ||
                                        _api == null
                                    ? null
                                    : _choose,
                                icon: const Icon(
                                  Icons.add_photo_alternate_outlined,
                                ),
                                label: const Text('相册添加'),
                              ),
                              TextButton(
                                onPressed: _busy
                                    ? null
                                    : () => setState(() {
                                        _managing = !_managing;
                                        _selected.clear();
                                      }),
                                child: Text(_managing ? '完成管理' : '管理'),
                              ),
                              if (_managing)
                                TextButton(
                                  onPressed: _busy || _selected.isEmpty
                                      ? null
                                      : _remove,
                                  child: Text('删除所选（${_selected.length}）'),
                                ),
                            ],
                          ),
                        ),
                        if (_busy || _loading) const LinearProgressIndicator(),
                        Expanded(
                          child: SingleChildScrollView(
                            padding: const EdgeInsets.all(12),
                            child: Column(
                              children: [
                                if (_error != null)
                                  Semantics(
                                    liveRegion: true,
                                    child: Text(
                                      _error!,
                                      style: const TextStyle(
                                        color: AppColors.secondary,
                                      ),
                                    ),
                                  ),
                                if (!_loading)
                                  TextButton(
                                    onPressed: _busy ? null : _load,
                                    child: const Text('重新读取'),
                                  ),
                                if (_snapshot?.pendingUpload != null)
                                  Card(
                                    child: Padding(
                                      padding: const EdgeInsets.all(12),
                                      child: Column(
                                        children: [
                                          const Text(
                                            '有一次上传尚未完成保存。结束此记录不会发送表情，也不会删除可能已经上传的文件。',
                                          ),
                                          TextButton(
                                            onPressed: _busy || _receipt != null
                                                ? null
                                                : _endPending,
                                            child: const Text('结束此上传记录'),
                                          ),
                                        ],
                                      ),
                                    ),
                                  ),
                                if (_remaining.isNotEmpty)
                                  Text('还有 ${_remaining.length} 张图片尚未开始保存'),
                                if (_preview != null)
                                  Column(
                                    children: [
                                      Wrap(
                                        spacing: 8,
                                        runSpacing: 8,
                                        children: [
                                          Image.file(
                                            _preview!.file,
                                            width: 100,
                                            height: 100,
                                            semanticLabel: '待保存的常用表情预览',
                                          ),
                                          for (final file in _remaining)
                                            Image.file(
                                              file.file,
                                              width: 64,
                                              height: 64,
                                              semanticLabel: '尚未保存的常用表情预览',
                                            ),
                                        ],
                                      ),
                                      const Text(
                                        '将逐一保存，遇到失败或结果未确认时暂停；已保存的表情会保留。',
                                      ),
                                      FilledButton(
                                        onPressed: _busy ? null : _saveGallery,
                                        child: Text(
                                          _receipt == null
                                              ? '保存到常用表情'
                                              : '重试保存已上传图片',
                                        ),
                                      ),
                                    ],
                                  ),
                                if (!_loading &&
                                    _error == null &&
                                    _snapshot?.entries.isEmpty == true)
                                  const Padding(
                                    padding: EdgeInsets.all(18),
                                    child: Text('暂无常用表情，可从相册或图片消息添加'),
                                  ),
                                Wrap(
                                  spacing: 8,
                                  runSpacing: 8,
                                  children: [
                                    for (final entry
                                        in _snapshot?.entries ?? <SavedEmoji>[])
                                      SizedBox(
                                        width: 88,
                                        child: Column(
                                          children: [
                                            if (_managing)
                                              Checkbox(
                                                value: _selected.contains(
                                                  entry.id,
                                                ),
                                                onChanged: _busy
                                                    ? null
                                                    : (_) => setState(() {
                                                        if (!_selected.add(
                                                          entry.id,
                                                        )) {
                                                          _selected.remove(
                                                            entry.id,
                                                          );
                                                        }
                                                      }),
                                              ),
                                            IconButton(
                                              tooltip: _managing
                                                  ? '选择常用表情'
                                                  : '发送常用表情',
                                              constraints: const BoxConstraints(
                                                minWidth: 72,
                                                minHeight: 72,
                                              ),
                                              visualDensity:
                                                  VisualDensity.standard,
                                              onPressed:
                                                  _busy ||
                                                      !_visible(entry) ||
                                                      _managing
                                                  ? null
                                                  : () => _send(entry),
                                              icon: _visible(entry)
                                                  ? Image(
                                                      image: ref.read(
                                                        savedEmojiImageProvider,
                                                      )(entry.url),
                                                      width: 64,
                                                      height: 64,
                                                      errorBuilder: (_, _, _) =>
                                                          const Text(
                                                            '图片加载失败，请重新读取',
                                                          ),
                                                    )
                                                  : const Text('原消息不可保留'),
                                            ),
                                          ],
                                        ),
                                      ),
                                  ],
                                ),
                              ],
                            ),
                          ),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Register one monotonically retired portal ticket; normal pop is allowed,
/// covering this owned portal with a different route never renews it after ABA.
class EmojiPortalGuard extends StatefulWidget {
  const EmojiPortalGuard({
    super.key,
    required this.onCovered,
    required this.child,
  });
  final VoidCallback onCovered;
  final Widget child;
  @override
  State<EmojiPortalGuard> createState() => _EmojiPortalGuardState();
}

class _EmojiPortalGuardState extends State<EmojiPortalGuard> {
  bool _retired = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (!_retired && route?.isActive == true && route?.isCurrent == false) {
      _retired = true;
      widget.onCovered();
    }
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
