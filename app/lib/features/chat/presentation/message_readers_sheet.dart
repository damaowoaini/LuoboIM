import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/tokens.dart';
import '../data/chat_repository.dart';
import '../domain/chat_message.dart';

class MessageReadersSheet extends StatefulWidget {
  const MessageReadersSheet({
    super.key,
    required this.repository,
    required this.messageId,
  });
  final ChatRepository repository;
  final String messageId;

  @override
  State<MessageReadersSheet> createState() => _MessageReadersSheetState();
}

class _MessageReadersSheetState extends State<MessageReadersSheet> {
  final List<MessageReader> _readers = [];
  late final StreamSubscription<int> _invalidations;
  bool _read = true;
  bool _loading = false;
  bool _hasMore = true;
  int _offset = 0;
  int _request = 0;
  AppFailure? _failure;

  @override
  void initState() {
    super.initState();
    _invalidations = widget.repository.session.invalidations.listen((_) {
      _request++;
      _readers.clear();
      if (mounted) unawaited(Navigator.of(context).maybePop());
    });
    unawaited(_load());
  }

  @override
  void dispose() {
    _request++;
    unawaited(_invalidations.cancel());
    super.dispose();
  }

  Future<void> _load({bool reset = false}) async {
    if (_loading && !reset) return;
    final request = ++_request;
    setState(() {
      if (reset) {
        _readers.clear();
        _offset = 0;
        _hasMore = true;
      }
      _loading = true;
      _failure = null;
    });
    try {
      final page = await widget.repository.readers(
        widget.messageId,
        read: _read,
        offset: _offset,
      );
      if (!mounted || request != _request) return;
      setState(() {
        final existing = _readers.map((reader) => reader.userId).toSet();
        _readers.addAll(
          page.readers.where((reader) => existing.add(reader.userId)),
        );
        _offset = page.nextOffset;
        _hasMore = page.hasMore;
      });
    } catch (error) {
      if (!mounted || request != _request) return;
      setState(() {
        _failure = error is AppFailure
            ? error
            : const AppFailure(FailureKind.unknown);
      });
    } finally {
      if (mounted && request == _request) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: SizedBox(
      height: MediaQuery.sizeOf(context).height * .72,
      child: Column(
        children: [
          const SizedBox(height: 14),
          const Text(
            '群消息阅读情况',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
          ),
          Padding(
            padding: const EdgeInsets.all(16),
            child: SegmentedButton<bool>(
              segments: const [
                ButtonSegment(value: true, label: Text('已读成员')),
                ButtonSegment(value: false, label: Text('未读成员')),
              ],
              selected: {_read},
              onSelectionChanged: (selection) {
                setState(() => _read = selection.single);
                unawaited(_load(reset: true));
              },
            ),
          ),
          Expanded(
            child: RefreshIndicator(
              onRefresh: () => _load(reset: true),
              child: ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                children: [
                  for (final reader in _readers)
                    ListTile(
                      leading: CircleAvatar(
                        backgroundColor: AppColors.primary.withValues(
                          alpha: .08,
                        ),
                        foregroundImage: reader.avatar == null
                            ? null
                            : NetworkImage(reader.avatar.toString()),
                        child: Text(reader.name.characters.first),
                      ),
                      title: Text(reader.name),
                      onTap: () => Navigator.of(context).pop(reader.userId),
                    ),
                  if (_failure != null)
                    Padding(
                      padding: const EdgeInsets.all(20),
                      child: Column(
                        children: [
                          Text(_failure!.message),
                          TextButton(
                            onPressed: _loading ? null : _load,
                            child: const Text('重试'),
                          ),
                        ],
                      ),
                    )
                  else if (!_loading && _readers.isEmpty)
                    Padding(
                      padding: const EdgeInsets.all(28),
                      child: Text(_read ? '暂时没有已读成员，可下拉刷新' : '暂时没有未读成员'),
                    ),
                  if (_loading)
                    const Center(
                      child: Padding(
                        padding: EdgeInsets.all(24),
                        child: CircularProgressIndicator(),
                      ),
                    )
                  else if (_hasMore && _failure == null && _readers.isNotEmpty)
                    TextButton(onPressed: _load, child: const Text('加载更多成员')),
                ],
              ),
            ),
          ),
        ],
      ),
    ),
  );
}
