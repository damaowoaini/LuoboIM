import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../account_providers.dart';
import '../data/account_repository.dart';

class AccountSettingsPage extends ConsumerStatefulWidget {
  const AccountSettingsPage({super.key});
  @override
  ConsumerState<AccountSettingsPage> createState() =>
      _AccountSettingsPageState();
}

class _AccountSettingsPageState extends ConsumerState<AccountSettingsPage> {
  bool busy = false;
  bool historyOutcome = false;
  String? failure;
  String? notice;
  Future<void> change(
    Future<AccountSaveResult> Function(AccountRepository) action,
  ) async {
    final repository = ref.read(accountRepositoryProvider);
    if (repository == null || busy) return;
    setState(() {
      busy = true;
      failure = null;
      notice = null;
      historyOutcome = false;
    });
    try {
      final result = await action(repository);
      if (mounted && repository.isCurrent) {
        ref.invalidate(accountProfileProvider);
        if (result.imSyncPending) {
          setState(() => notice = '设置已保存，部分资料仍在同步，可稍后刷新核对');
        }
      }
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(
          () => failure = error is AppFailure ? error.message : '设置未能确认，请刷新核对',
        );
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> clearHistory() async {
    final repository = ref.read(accountRepositoryProvider);
    if (repository == null || busy || !repository.isCurrent) return;
    final confirmed = await confirmAction(
      context,
      '清空全部聊天记录？',
      '将清理当前账号在本机及服务端可见的全部会话历史，清理后无法恢复。',
      action: '清空记录',
    );
    if (!confirmed || !mounted || !repository.isCurrent || busy) return;
    setState(() {
      busy = true;
      failure = null;
      notice = null;
      historyOutcome = false;
    });
    try {
      await repository.clearAllHistory();
      if (mounted && repository.isCurrent) {
        setState(() {
          notice = '清理请求已完成，请返回会话列表核对。';
          historyOutcome = true;
        });
      }
    } catch (_) {
      if (mounted && repository.isCurrent) {
        setState(() {
          failure = '清理结果尚未确认，部分记录可能已清理。请返回会话列表核对，避免连续重试。';
          historyOutcome = true;
        });
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !busy,
    child: Scaffold(
      appBar: AppBar(
        title: const Text('消息与隐私'),
        actions: [
          IconButton(
            tooltip: '刷新设置',
            onPressed: busy
                ? null
                : () => ref.invalidate(accountProfileProvider),
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: ref
          .watch(accountProfileProvider)
          .when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (error, _) => Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(error is AppFailure ? error.message : '暂时无法读取设置'),
                  TextButton(
                    onPressed: () => ref.invalidate(accountProfileProvider),
                    child: const Text('重试'),
                  ),
                ],
              ),
            ),
            data: (user) => ListView(
              padding: const EdgeInsets.all(16),
              children: [
                SwitchListTile(
                  title: const Text('全局免打扰'),
                  value: user.globalReceive != 0,
                  subtitle: Text(
                    user.globalReceive == 1
                        ? '当前已停止接收消息，关闭可恢复接收'
                        : '继续接收消息，不接收离线推送提醒',
                  ),
                  onChanged: busy
                      ? null
                      : (value) => change(
                          (repository) => repository.setDoNotDisturb(value),
                        ),
                ),
                for (final entry in const {
                  AccountPreference.sound: '声音提醒',
                  AccountPreference.vibration: '振动提醒',
                  AccountPreference.friendRequests: '允许别人添加我为好友',
                }.entries)
                  SwitchListTile(
                    title: Text(entry.value),
                    value: user.enabled(entry.key),
                    onChanged: busy
                        ? null
                        : (value) => change(
                            (repository) =>
                                repository.setPreference(entry.key, value),
                          ),
                  ),
                const Divider(),
                ListTile(
                  leading: const Icon(Icons.block_outlined),
                  title: const Text('黑名单'),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: busy ? null : () => context.push('/contacts/blocked'),
                ),
                const Divider(),
                ListTile(
                  leading: const Icon(Icons.delete_sweep_outlined),
                  title: const Text('清空全部聊天记录'),
                  subtitle: const Text('当前账号的全部会话，清理后无法恢复'),
                  onTap: busy ? null : clearHistory,
                ),
                if (busy) const LinearProgressIndicator(),
                if (notice != null)
                  Padding(
                    padding: const EdgeInsets.all(12),
                    child: Text(notice!),
                  ),
                if (failure != null)
                  Padding(
                    padding: const EdgeInsets.all(12),
                    child: Text(
                      failure!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
                if (historyOutcome)
                  TextButton(
                    onPressed: busy ? null : () => context.go('/home'),
                    child: const Text('查看会话'),
                  ),
              ],
            ),
          ),
    ),
  );
}
