import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../complaint_providers.dart';
import '../data/complaint_repository.dart';

class MyComplaintsPage extends ConsumerStatefulWidget {
  const MyComplaintsPage({super.key});
  @override
  ConsumerState<MyComplaintsPage> createState() => _MyComplaintsPageState();
}

class _MyComplaintsPageState extends ConsumerState<MyComplaintsPage> {
  late final ComplaintRepository? owner = ref.read(complaintRepositoryProvider);
  final records = <ComplaintRecord>[];
  bool loading = true;
  int total = 0, page = 0;
  String? failure;

  @override
  void initState() {
    super.initState();
    load(reset: true);
  }

  Future<void> load({bool reset = false}) async {
    final repository = owner;
    if (repository == null || !repository.isCurrent || (loading && page != 0)) {
      return;
    }
    setState(() {
      loading = true;
      failure = null;
    });
    try {
      final next = reset ? 1 : page + 1;
      final result = await repository.search(page: next);
      if (!mounted || !repository.isCurrent) return;
      setState(() {
        if (reset) records.clear();
        final ids = records.map((r) => r.id).toSet();
        records.addAll(result.records.where((r) => ids.add(r.id)));
        total = result.total;
        page = next;
      });
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(
          () => failure = error is AppFailure ? error.message : '暂时无法读取投诉',
        );
      }
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final current = ref.watch(complaintRepositoryProvider);
    if (owner == null || !identical(owner, current) || !owner!.isCurrent) {
      return const Scaffold(
        body: SafeArea(child: Center(child: Text('账号已变化，请返回后重新进入'))),
      );
    }
    return Scaffold(
      appBar: AppBar(
        title: const Text('我的投诉'),
        actions: [
          IconButton(
            tooltip: '刷新',
            onPressed: loading ? null : () => load(reset: true),
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 760),
            child: ListView(
              padding: const EdgeInsets.all(20),
              children: [
                if (!loading && records.isEmpty && failure == null)
                  const Padding(
                    padding: EdgeInsets.all(24),
                    child: Text('暂无投诉记录'),
                  ),
                for (final item in records)
                  Card(
                    child: ExpansionTile(
                      title: Text(
                        item.targetName.isEmpty
                            ? item.targetId
                            : item.targetName,
                      ),
                      subtitle: Text(
                        '${item.reason} · ${switch (item.status) {
                          1 => '已处理',
                          2 => '已驳回',
                          _ => '待处理',
                        }}',
                      ),
                      childrenPadding: const EdgeInsets.all(16),
                      expandedCrossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        SelectableText('投诉编号：${item.id}'),
                        if (item.createdAt != null)
                          Text('提交时间：${item.createdAt!.toLocal()}'),
                        if (item.description.isNotEmpty)
                          Padding(
                            padding: const EdgeInsets.symmetric(vertical: 8),
                            child: Text(item.description),
                          ),
                        if (item.result.isNotEmpty) Text('处理结果：${item.result}'),
                        if (item.evidence.isNotEmpty)
                          Wrap(
                            spacing: 8,
                            runSpacing: 8,
                            children: [
                              for (final uri in item.evidence)
                                Image.network(
                                  uri.toString(),
                                  width: 104,
                                  height: 104,
                                  fit: BoxFit.cover,
                                  cacheWidth: 208,
                                  errorBuilder: (_, _, _) => const SizedBox(
                                    width: 104,
                                    height: 104,
                                    child: Center(child: Text('图片不可用')),
                                  ),
                                ),
                            ],
                          ),
                      ],
                    ),
                  ),
                if (failure != null)
                  Padding(
                    padding: const EdgeInsets.all(16),
                    child: Text(
                      failure!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
                if (loading) const Center(child: CircularProgressIndicator()),
                if (!loading && (failure != null || records.length < total))
                  TextButton(
                    onPressed: load,
                    child: Text(failure != null ? '重试' : '加载更多'),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
