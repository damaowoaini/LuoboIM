import 'dart:async';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:share_plus/share_plus.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/app_runtime.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../data/personal_archive.dart';
import '../data/personal_repository.dart';
import '../domain/personal_item.dart';
import '../personal_provider.dart';

class PersonalPage extends ConsumerStatefulWidget {
  const PersonalPage({super.key, required this.kind});
  final PersonalKind kind;
  @override
  ConsumerState<PersonalPage> createState() => _PersonalPageState();
}

class _PersonalPageState extends ConsumerState<PersonalPage> {
  final search = TextEditingController();
  List<PersonalItem> items = [];
  bool loading = true;
  bool busy = false;
  String? error;
  PersonalRepository? _repository;
  int filter = 0;
  bool get notes => widget.kind == PersonalKind.note;

  @override
  void dispose() {
    search.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final repository = _repository;
    if (repository == null || !repository.isCurrent()) return;
    try {
      final values = await repository.list();
      if (!mounted ||
          !repository.isCurrent() ||
          !identical(repository, _repository)) {
        return;
      }
      setState(() {
        items = values.where((item) => item.kind == widget.kind).toList();
        error = null;
        loading = false;
      });
    } on AppFailure catch (failure) {
      if (mounted &&
          repository.isCurrent() &&
          identical(repository, _repository)) {
        setState(() {
          error = failure.message;
          loading = false;
        });
      }
    }
  }

  Future<void> _perform(Future<void> Function() action) async {
    if (busy) return;
    setState(() => busy = true);
    try {
      await action();
      await _load();
    } on AppFailure catch (failure) {
      if (mounted) _notice(failure.message);
    } catch (_) {
      if (mounted) _notice('操作未完成，请重试');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  void _notice(String text) =>
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));

  List<PersonalItem> get visible {
    final query = search.text.trim().toLowerCase();
    final result = items
        .where(
          (item) =>
              (filter == 0 ||
                  (filter == 1 ? !item.completed : item.completed)) &&
              (query.isEmpty ||
                  '${item.title}\n${item.body}\n${item.category}'
                      .toLowerCase()
                      .contains(query)),
        )
        .toList();
    result.sort((a, b) {
      if (notes) {
        if (a.pinned != b.pinned) return a.pinned ? -1 : 1;
        return b.updatedAt.compareTo(a.updatedAt);
      }
      if (a.completed != b.completed) return a.completed ? 1 : -1;
      if (a.priority != b.priority) return b.priority.compareTo(a.priority);
      if (a.dueAt != null && b.dueAt == null) return -1;
      if (a.dueAt == null && b.dueAt != null) return 1;
      if (a.dueAt != null && b.dueAt != null) {
        return a.dueAt!.compareTo(b.dueAt!);
      }
      return b.createdAt.compareTo(a.createdAt);
    });
    return result;
  }

  @override
  Widget build(BuildContext context) {
    if (ref.watch(appRuntimeProvider).phase != AppPhase.online) {
      return const SizedBox.shrink();
    }
    // Keep account-bound storage alive for the entire visible route, including
    // slow native I/O and an editor pushed above this page.
    final repository = ref.watch(personalRepositoryProvider);
    if (!identical(repository, _repository)) {
      _repository = repository;
      items = [];
      error = null;
      loading = true;
      scheduleMicrotask(() {
        if (mounted && identical(repository, _repository)) unawaited(_load());
      });
    }
    final records = visible;
    return Scaffold(
      appBar: AppBar(
        title: Text(notes ? '笔记' : '待办'),
        actions: [
          PopupMenuButton<String>(
            enabled: !busy,
            tooltip: '资料管理',
            onSelected: (action) => switch (action) {
              'import' => _perform(_import),
              'export' => _perform(_export),
              'clear' => _perform(_clearCompleted),
              _ => Future<void>.value(),
            },
            itemBuilder: (_) => [
              const PopupMenuItem(value: 'import', child: Text('导入笔记与待办')),
              const PopupMenuItem(value: 'export', child: Text('导出笔记与待办')),
              if (!notes)
                const PopupMenuItem(value: 'clear', child: Text('清理已完成')),
            ],
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: busy ? null : () => _edit(PersonalItem.create(widget.kind)),
        icon: const Icon(Icons.add),
        label: Text(notes ? '写笔记' : '添加待办'),
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 800),
            child: Column(
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
                  child: TextField(
                    controller: search,
                    onChanged: (_) => setState(() {}),
                    decoration: InputDecoration(
                      prefixIcon: const Icon(Icons.search),
                      hintText: notes ? '搜索笔记内容或分类' : '搜索待办内容或分类',
                    ),
                  ),
                ),
                if (!notes)
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: Wrap(
                      spacing: 8,
                      children: [
                        for (final entry in [
                          (0, '全部 ${items.length}'),
                          (
                            1,
                            '未完成 ${items.where((item) => !item.completed).length}',
                          ),
                          (
                            2,
                            '已完成 ${items.where((item) => item.completed).length}',
                          ),
                        ])
                          ChoiceChip(
                            label: Text(entry.$2),
                            selected: filter == entry.$1,
                            onSelected: (_) =>
                                setState(() => filter = entry.$1),
                          ),
                        if (items.any(
                          (item) =>
                              !item.completed &&
                              item.dueAt?.isBefore(DateTime.now()) == true,
                        ))
                          Chip(
                            label: Text(
                              '逾期 ${items.where((item) => !item.completed && item.dueAt?.isBefore(DateTime.now()) == true).length}',
                            ),
                            avatar: const Icon(
                              Icons.schedule,
                              size: 18,
                              color: AppColors.error,
                            ),
                          ),
                      ],
                    ),
                  ),
                if (busy) const LinearProgressIndicator(),
                if (error != null)
                  MaterialBanner(
                    content: Text(error!),
                    actions: [
                      TextButton(onPressed: _load, child: const Text('重试')),
                    ],
                  ),
                Expanded(
                  child: loading
                      ? const Center(child: CircularProgressIndicator())
                      : RefreshIndicator(
                          onRefresh: _load,
                          child: records.isEmpty
                              ? ListView(
                                  physics:
                                      const AlwaysScrollableScrollPhysics(),
                                  children: [
                                    Padding(
                                      padding: const EdgeInsets.all(48),
                                      child: Text(
                                        search.text.isNotEmpty
                                            ? '没有找到匹配的内容'
                                            : notes
                                            ? '还没有笔记，记录一点想法吧'
                                            : '当前没有待办',
                                        textAlign: TextAlign.center,
                                        style: const TextStyle(
                                          color: AppColors.secondary,
                                        ),
                                      ),
                                    ),
                                  ],
                                )
                              : ListView.builder(
                                  padding: const EdgeInsets.fromLTRB(
                                    16,
                                    8,
                                    16,
                                    100,
                                  ),
                                  itemCount: records.length,
                                  itemBuilder: (_, index) =>
                                      _row(records[index]),
                                ),
                        ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _row(PersonalItem item) {
    final overdue =
        !item.completed &&
        item.dueAt != null &&
        item.dueAt!.isBefore(DateTime.now());
    return Card(
      color: Colors.white,
      elevation: 0,
      child: ListTile(
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 16,
          vertical: 10,
        ),
        leading: notes
            ? (item.pinned
                  ? const Icon(
                      Icons.push_pin_outlined,
                      color: AppColors.primary,
                    )
                  : null)
            : Checkbox(
                value: item.completed,
                onChanged: busy
                    ? null
                    : (value) => _perform(
                        () => ref
                            .read(personalRepositoryProvider)
                            .save(item.copyWith(completed: value)),
                      ),
              ),
        title: Text(
          item.title.isEmpty ? '无标题' : item.title,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            fontWeight: FontWeight.w600,
            decoration: item.completed ? TextDecoration.lineThrough : null,
          ),
        ),
        subtitle: Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (item.body.isNotEmpty)
                Text(item.body, maxLines: 2, overflow: TextOverflow.ellipsis),
              const SizedBox(height: 6),
              Wrap(
                spacing: 10,
                children: [
                  if (item.category.isNotEmpty)
                    Text(
                      item.category,
                      style: const TextStyle(color: AppColors.secondary),
                    ),
                  if (!notes)
                    Text(
                      ['低优先级', '中优先级', '高优先级'][item.priority],
                      style: TextStyle(
                        color: item.priority == 2
                            ? AppColors.error
                            : AppColors.secondary,
                      ),
                    ),
                  if (item.dueAt != null)
                    Text(
                      '${overdue ? '已逾期 · ' : ''}${_date(item.dueAt!)}',
                      style: TextStyle(
                        color: overdue ? AppColors.error : AppColors.secondary,
                      ),
                    ),
                ],
              ),
            ],
          ),
        ),
        onTap: busy ? null : () => _edit(item),
        trailing: PopupMenuButton<String>(
          tooltip: '更多操作',
          enabled: !busy,
          onSelected: (action) => _perform(() async {
            final repository = ref.read(personalRepositoryProvider);
            if (action == 'pin') {
              await repository.save(item.copyWith(pinned: !item.pinned));
            }
            if (action == 'delete' &&
                await _confirm('删除这${notes ? '篇笔记' : '条待办'}？', '此操作无法撤销。')) {
              await repository.delete(item);
            }
          }),
          itemBuilder: (_) => [
            if (notes)
              PopupMenuItem(
                value: 'pin',
                child: Text(item.pinned ? '取消置顶' : '置顶'),
              ),
            const PopupMenuItem(
              value: 'delete',
              child: Text('删除', style: TextStyle(color: AppColors.error)),
            ),
          ],
        ),
      ),
    );
  }

  Future<bool> _confirm(String title, String body) async =>
      await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(title),
          content: Text(body),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('确认'),
            ),
          ],
        ),
      ) ??
      false;

  Future<void> _edit(PersonalItem item) async {
    final repository = ref.read(personalRepositoryProvider);
    final result = await Navigator.of(context).push<PersonalItem>(
      MaterialPageRoute(
        builder: (_) => PersonalEditor(item: item, onSave: repository.save),
      ),
    );
    if (result != null && mounted) await _load();
  }

  Future<void> _import() async {
    final repository = ref.read(personalRepositoryProvider);
    if (!repository.isCurrent()) return;
    final file = await openFile(
      acceptedTypeGroups: const [
        XTypeGroup(
          label: '资料备份',
          extensions: ['json'],
          uniformTypeIdentifiers: ['public.json'],
        ),
      ],
    );
    if (file == null || !mounted || !repository.isCurrent()) return;
    final length = await file.length();
    if (!mounted || !repository.isCurrent()) return;
    if (length > PersonalArchive.maxBytes) {
      _notice('文件超过 16MB，请分批导入');
      return;
    }
    late PersonalArchive archive;
    try {
      final bytes = await file.readAsBytes();
      if (!mounted || !repository.isCurrent()) return;
      archive = PersonalArchive.decode(bytes);
    } on AppFailure {
      if (mounted && repository.isCurrent()) {
        _notice('备份格式不正确或包含损坏数据，未导入任何资料');
      }
      return;
    }
    if (!mounted || !repository.isCurrent()) return;
    final needsConfirmation = archive.owner != repository.accountKey;
    var confirmed = !needsConfirmation;
    final proceed = await showDialog<bool>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('导入本地资料'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '笔记 ${archive.items.where((item) => item.kind == PersonalKind.note).length} 篇，待办 ${archive.items.where((item) => item.kind == PersonalKind.todo).length} 条。',
                ),
                const SizedBox(height: 12),
                const Text('现有内容会保留。相同标识但内容不同的资料将另存为副本，原备份文件不会删除。'),
                if (needsConfirmation)
                  CheckboxListTile(
                    contentPadding: EdgeInsets.zero,
                    value: confirmed,
                    title: Text(
                      archive.owner == null
                          ? '旧版资料没有账号归属，我确认导入当前账号'
                          : '这份备份来自其他账号，我确认导入当前账号',
                    ),
                    onChanged: (value) =>
                        setDialogState(() => confirmed = value ?? false),
                  ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: confirmed ? () => Navigator.pop(context, true) : null,
              child: const Text('导入'),
            ),
          ],
        ),
      ),
    );
    if (proceed != true || !mounted || !repository.isCurrent()) return;
    final count = await repository.import(archive, confirmOwnership: confirmed);
    if (mounted && repository.isCurrent()) {
      _notice(count == 0 ? '这份备份已导入，未重复添加' : '已导入 $count 条资料');
    }
  }

  Future<void> _clearCompleted() async {
    final repository = ref.read(personalRepositoryProvider);
    if (!repository.isCurrent()) return;
    final confirmed = await _confirm('删除已完成的待办？', '未完成的待办会保留，此操作无法撤销。');
    if (!confirmed || !mounted || !repository.isCurrent()) return;
    await repository.deleteCompleted();
  }

  Future<void> _export() async {
    final repository = ref.read(personalRepositoryProvider);
    final parts = await repository.exportParts();
    if (!mounted || !repository.isCurrent()) return;
    final box = context.findRenderObject() as RenderBox?;
    final name = 'luobo-personal-${DateTime.now().millisecondsSinceEpoch}';
    final result = await SharePlus.instance.share(
      ShareParams(
        title: '萝卜对话本地资料',
        files: [
          for (final data in parts)
            XFile.fromData(
              Uint8List.fromList(data),
              mimeType: 'application/json',
            ),
        ],
        fileNameOverrides: [
          for (var index = 0; index < parts.length; index++)
            parts.length == 1
                ? '$name.json'
                : '$name-part-${index + 1}-of-${parts.length}.json',
        ],
        sharePositionOrigin: box == null
            ? null
            : box.localToGlobal(Offset.zero) & box.size,
      ),
    );
    if (mounted &&
        repository.isCurrent() &&
        result.status == ShareResultStatus.success) {
      _notice(
        parts.length == 1
            ? '已导出，请妥善保存备份'
            : '已导出 ${parts.length} 份备份，请保存全部文件，恢复时逐份导入',
      );
    }
  }

  String _date(DateTime date) {
    final local = date.toLocal();
    return '${local.year}/${local.month}/${local.day}';
  }
}

class PersonalEditor extends StatefulWidget {
  const PersonalEditor({super.key, required this.item, required this.onSave});
  final PersonalItem item;
  final Future<void> Function(PersonalItem item) onSave;
  @override
  State<PersonalEditor> createState() => _PersonalEditorState();
}

class _PersonalEditorState extends State<PersonalEditor> {
  late final title = TextEditingController(text: widget.item.title);
  late final body = TextEditingController(text: widget.item.body);
  late final category = TextEditingController(text: widget.item.category);
  late int priority = widget.item.priority;
  late DateTime? due = widget.item.dueAt;
  bool leaving = false;
  bool saving = false;
  bool get changed =>
      title.text != widget.item.title ||
      body.text != widget.item.body ||
      category.text != widget.item.category ||
      priority != widget.item.priority ||
      due != widget.item.dueAt;
  @override
  void dispose() {
    title.dispose();
    body.dispose();
    category.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: leaving || (!changed && !saving),
    onPopInvokedWithResult: (didPop, _) async {
      if (didPop || saving) return;
      final discard = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('放弃未保存的修改？'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('继续编辑'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('放弃'),
            ),
          ],
        ),
      );
      if (discard == true && context.mounted) {
        setState(() => leaving = true);
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (context.mounted) Navigator.pop(context);
        });
      }
    },
    child: Scaffold(
      appBar: AppBar(
        title: Text(widget.item.kind == PersonalKind.note ? '编辑笔记' : '编辑待办'),
        actions: [
          TextButton(
            onPressed: saving
                ? null
                : () async {
                    if (title.text.trim().isEmpty && body.text.trim().isEmpty) {
                      ScaffoldMessenger.of(
                        context,
                      ).showSnackBar(const SnackBar(content: Text('请填写标题或内容')));
                      return;
                    }
                    final result = widget.item.copyWith(
                      title: title.text.trim(),
                      body: body.text,
                      category: category.text.trim(),
                      priority: priority,
                      dueAt: due,
                      clearDue: due == null,
                    );
                    setState(() => saving = true);
                    try {
                      await widget.onSave(result);
                      if (!mounted) return;
                      setState(() {
                        leaving = true;
                        saving = false;
                      });
                      WidgetsBinding.instance.addPostFrameCallback((_) {
                        if (context.mounted) Navigator.pop(context, result);
                      });
                    } catch (_) {
                      if (context.mounted) {
                        setState(() => saving = false);
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(content: Text('保存失败，内容已保留在此页面，请重试')),
                        );
                      }
                    }
                  },
            child: const Text('保存'),
          ),
        ],
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 800),
            child: AbsorbPointer(
              absorbing: saving,
              child: ListView(
                padding: const EdgeInsets.all(20),
                children: [
                  TextField(
                    controller: title,
                    maxLength: 1000,
                    decoration: const InputDecoration(labelText: '标题'),
                    onChanged: (_) => setState(() {}),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: body,
                    minLines: 8,
                    maxLines: null,
                    maxLength: 1000000,
                    decoration: const InputDecoration(labelText: '内容'),
                    onChanged: (_) => setState(() {}),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: category,
                    maxLength: 200,
                    decoration: const InputDecoration(labelText: '分类'),
                    onChanged: (_) => setState(() {}),
                  ),
                  if (widget.item.kind == PersonalKind.todo) ...[
                    const SizedBox(height: 16),
                    DropdownButtonFormField<int>(
                      initialValue: priority,
                      decoration: const InputDecoration(labelText: '优先级'),
                      items: [
                        for (var i = 0; i < 3; i++)
                          DropdownMenuItem(
                            value: i,
                            child: Text(['低', '中', '高'][i]),
                          ),
                      ],
                      onChanged: (value) =>
                          setState(() => priority = value ?? 1),
                    ),
                    const SizedBox(height: 12),
                    ListTile(
                      title: Text(
                        due == null
                            ? '设置截止日期'
                            : '截止日期 ${due!.toLocal().toString().split(' ').first}',
                      ),
                      leading: const Icon(Icons.event_outlined),
                      trailing: due == null
                          ? null
                          : IconButton(
                              tooltip: '清除日期',
                              onPressed: () => setState(() => due = null),
                              icon: const Icon(Icons.close),
                            ),
                      onTap: () async {
                        final first = DateTime(1970);
                        final last = DateTime(9999, 12, 31);
                        final initial = (due ?? DateTime.now()).toLocal();
                        final selected = await showDatePicker(
                          context: context,
                          initialDate: initial.isBefore(first)
                              ? first
                              : initial.isAfter(last)
                              ? last
                              : initial,
                          firstDate: first,
                          lastDate: last,
                        );
                        if (selected != null && mounted) {
                          setState(
                            () => due = DateTime(
                              selected.year,
                              selected.month,
                              selected.day,
                              23,
                              59,
                              59,
                            ).toUtc(),
                          );
                        }
                      },
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
    ),
  );
}
