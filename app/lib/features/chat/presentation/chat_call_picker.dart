import 'package:flutter/material.dart';

import '../data/chat_repository.dart';
import '../domain/chat_snapshot.dart';

class ChatCallPicker extends StatefulWidget {
  const ChatCallPicker({super.key, required this.repository});
  final ChatRepository repository;
  @override
  State<ChatCallPicker> createState() => _ChatCallPickerState();
}

class _ChatCallPickerState extends State<ChatCallPicker> {
  final members = <ChatCallCandidate>[];
  final selected = <String>{};
  bool loading = false, end = false, failed = false;
  String query = '';
  int offset = 0;
  @override
  void initState() {
    super.initState();
    _more();
  }

  Future<void> _more() async {
    if (loading || end) return;
    setState(() {
      loading = true;
      failed = false;
    });
    try {
      final result = await widget.repository.callCandidates(offset: offset);
      if (!mounted) return;
      setState(() {
        offset += 100;
        end = result.length < 99; // current account may occupy one raw row.
        final known = members.map((member) => member.id).toSet();
        members.addAll(result.where((member) => known.add(member.id)));
      });
    } catch (_) {
      if (mounted) setState(() => failed = true);
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('邀请群成员'),
    content: SizedBox(
      width: 420,
      height: 360,
      child: Column(
        children: [
          TextField(
            decoration: const InputDecoration(hintText: '搜索已加载成员'),
            onChanged: (value) => setState(() => query = value),
          ),
          Text('已选择 ${selected.length}/9 人'),
          Expanded(
            child: ListView(
              children: [
                for (final member in members.where(
                  (member) =>
                      member.name.contains(query) || member.id.contains(query),
                ))
                  CheckboxListTile(
                    value: selected.contains(member.id),
                    title: Text(member.name),
                    onChanged: (value) {
                      setState(() {
                        if (value == false) {
                          selected.remove(member.id);
                        } else if (selected.length < 9) {
                          selected.add(member.id);
                        }
                      });
                    },
                  ),
                if (loading) const Center(child: CircularProgressIndicator()),
                if (failed) const Text('成员加载失败，请重试'),
                if (!end && !loading)
                  TextButton(
                    onPressed: _more,
                    child: Text(failed ? '重试' : '加载更多成员'),
                  ),
              ],
            ),
          ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: selected.isEmpty
            ? null
            : () => Navigator.pop(context, selected.toList()),
        child: const Text('下一步'),
      ),
    ],
  );
}
