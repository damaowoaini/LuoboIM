import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../contacts/contacts_providers.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../../chat/presentation/message_privacy_content.dart';
import '../data/global_search_repository.dart';
import '../domain/global_search.dart';
import 'global_search_controller.dart';
import 'global_search_file_page.dart';
import 'global_search_matches_page.dart';

class GlobalSearchPage extends ConsumerStatefulWidget {
  const GlobalSearchPage({super.key, this.now});
  final DateTime Function()? now;
  @override
  ConsumerState<GlobalSearchPage> createState() => _GlobalSearchPageState();
}

class _GlobalSearchPageState extends ConsumerState<GlobalSearchPage>
    with SingleTickerProviderStateMixin {
  final input = TextEditingController();
  late final tabs = TabController(length: 5, vsync: this);
  GlobalSearchController? controller;
  Timer? debounce;
  static const labels = {
    GlobalSearchKind.contacts: '联系人', GlobalSearchKind.groups: '群聊',
    GlobalSearchKind.text: '聊天记录', GlobalSearchKind.files: '文件',
  };
  void changed() {
    if (!mounted) return;
    if (controller?.usable != true) { debounce?.cancel(); input.clear(); }
    setState(() {});
  }
  void search() {
    debounce?.cancel();
    if (ModalRoute.of(context)?.isCurrent == true) {
      unawaited(controller?.search(input.text));
    }
  }
  bool get active => mounted && controller?.usable == true &&
      ModalRoute.of(context)?.isCurrent == true;
  @override
  void dispose() {
    debounce?.cancel(); tabs.dispose(); input.dispose();
    controller?.removeListener(changed); controller?.dispose();
    super.dispose();
  }
  @override
  Widget build(BuildContext context) {
    final contacts = ref.watch(contactsRepositoryProvider);
    if (controller == null && contacts != null) {
      controller = GlobalSearchController(GlobalSearchRepository(
          contacts.gateway, contacts.access, contacts, now: widget.now))..addListener(changed);
    }
    final c = controller;
    return Scaffold(
      appBar: AppBar(title: const Text('全局搜索')),
      body: c == null || !c.usable
          ? const Center(child: Text('当前会话已失效，请重新登录后搜索'))
          : SafeArea(child: Column(children: [
              Padding(padding: const EdgeInsets.fromLTRB(16, 8, 16, 4), child: TextField(
                controller: input, maxLength: 200, textInputAction: TextInputAction.search,
                decoration: InputDecoration(labelText: '搜索联系人、群聊、文字或文件名', counterText: '',
                    suffixIcon: IconButton(tooltip: '开始全局搜索', onPressed: search, icon: const Icon(Icons.search))),
                onSubmitted: (_) => search(), onChanged: (_) {
                  debounce?.cancel();
                  // Retire results/navigation synchronously, not 350ms later.
                  unawaited(c.search(''));
                  debounce = Timer(const Duration(milliseconds: 350), search);
                },
              )),
              const Padding(padding: EdgeInsets.symmetric(horizontal: 16, vertical: 4),
                  child: Text('消息搜索范围为本账号已同步到本机的记录。')),
              TabBar(controller: tabs, isScrollable: true, tabs: const [
                Tab(text: '全部'), Tab(text: '联系人'), Tab(text: '群聊'),
                Tab(text: '聊天记录'), Tab(text: '文件'),
              ]),
              if (c.historyNotice != null) Padding(padding: const EdgeInsets.all(8), child: Text(c.historyNotice!)),
              Expanded(child: TabBarView(controller: tabs, children: [
                ListView(children: [for (final kind in GlobalSearchKind.values) ...section(kind, true)]),
                for (final kind in GlobalSearchKind.values) ListView(children: section(kind, false)),
              ])),
            ])),
    );
  }

  List<Widget> section(GlobalSearchKind kind, bool preview) {
    final c = controller!;
    final phase = c.phases[kind]!;
    final threads = kind == GlobalSearchKind.text ? c.threads : const <GlobalMessageGroup>[];
    final files = kind == GlobalSearchKind.files ? c.visibleFiles : const <GlobalMessageHit>[];
    final count = switch (kind) {
      GlobalSearchKind.contacts => c.contacts.length,
      GlobalSearchKind.groups => c.groups.length,
      GlobalSearchKind.text => threads.length,
      GlobalSearchKind.files => files.length,
    };
    final limit = preview ? 2 : c.shown[kind]!;
    return [
      ListTile(title: Text(labels[kind]!), trailing: preview && count > 2
          ? TextButton(onPressed: () => tabs.index = kind.index + 1, child: const Text('查看更多')) : null),
      if (phase == GlobalSearchPhase.loading) const LinearProgressIndicator(),
      if (phase == GlobalSearchPhase.error) Padding(padding: const EdgeInsets.all(12), child: Column(children: [
        Text(c.failures[kind]!.message),
        TextButton(onPressed: active ? () => c.retry(kind) : null, child: const Text('重试此类搜索')),
      ])),
      if (phase == GlobalSearchPhase.idle) const Padding(padding: EdgeInsets.all(12), child: Text('输入关键词后搜索')),
      if (phase == GlobalSearchPhase.ready && count == 0) const Padding(padding: EdgeInsets.all(12), child: Text('没有找到匹配结果')),
      if (kind == GlobalSearchKind.contacts)
        for (final person in c.contacts.take(limit)) ListTile(
          leading: ContactAvatar(uri: person.avatar), title: Text(person.displayName), subtitle: Text(person.id),
          onTap: () { if (active && c.contacts.any((row) => identical(row, person))) context.push(contactPath(person.id)); },
        ),
      if (kind == GlobalSearchKind.groups)
        for (final group in c.groups.take(limit)) ListTile(
          leading: ContactAvatar(uri: group.avatar, group: true), title: Text(group.name), subtitle: Text(group.id),
          onTap: () { if (active && c.groups.any((row) => identical(row, group))) context.push(groupPath(group.id)); },
        ),
      if (kind == GlobalSearchKind.text)
        for (final group in threads.take(limit)) MessagePrivacyContent(
          key: ValueKey('thread:${group.hits.first.key}'),
          privacy: group.hits.first.message.privacy, now: c.repository.now,
          child: ListTile(
          title: Text(group.title), subtitle: Text('${group.conversationId}\n${group.hits.first.summary}', maxLines: 4, overflow: TextOverflow.ellipsis),
          trailing: Text('${group.hits.length} 条'),
          onTap: () {
            if (active && group.hits.any((hit) => hit.visibleAt(c.repository.now()))) {
              final epoch = c.epoch;
              Navigator.push(context, MaterialPageRoute<void>(builder: (_) => GlobalSearchMatchesPage(
                  controller: c, conversationId: group.conversationId, queryEpoch: epoch)));
            }
          },
        )),
      if (kind == GlobalSearchKind.files)
        for (final hit in files.take(limit)) MessagePrivacyContent(
          key: ValueKey('file:${hit.key}'), privacy: hit.message.privacy,
          now: c.repository.now, child: ListTile(
          leading: const Icon(Icons.insert_drive_file_outlined), title: Text(hit.summary), subtitle: Text('${hit.title}\n${hit.conversationId}'),
          onTap: () { if (active && hit.visibleAt(c.repository.now())) Navigator.push(context, MaterialPageRoute<void>(builder: (_) => GlobalSearchFilePage(controller: c, hit: hit))); },
        )),
      if (!preview && limit < count) TextButton(onPressed: active ? () => c.more(kind) : null, child: const Text('显示更多结果')),
    ];
  }
}
