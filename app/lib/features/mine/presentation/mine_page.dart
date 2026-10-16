import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../data/mine_repository.dart';
import '../../moments/moments_providers.dart';

final mineProfileProvider = FutureProvider.autoDispose<MineProfile>((ref) {
  final runtime = ref.watch(appRuntimeProvider);
  return MineRepository(
    ref.watch(imGatewayProvider),
    runtime.session.state.generation,
  ).profile();
});

class MinePage extends ConsumerWidget {
  const MinePage({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final runtime = ref.watch(appRuntimeProvider);
    final credentials = runtime.session.state.credentials;
    final profile = ref.watch(mineProfileProvider);
    final momentUnread = ref.watch(momentUnreadProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('我的')),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 800),
            child: ListView(
              padding: const EdgeInsets.all(20),
              children: [
                Card(
                  color: Colors.white,
                  elevation: 0,
                  child: Padding(
                    padding: const EdgeInsets.all(20),
                    child: profile.when(
                      data: (user) => Row(
                        children: [
                          CircleAvatar(
                            radius: 30,
                            backgroundColor: const Color(0xffdfeaff),
                            backgroundImage: user.avatar == null
                                ? null
                                : NetworkImage(user.avatar.toString()),
                            onBackgroundImageError: user.avatar == null
                                ? null
                                : (_, _) {},
                            child: user.avatar == null
                                ? const Icon(
                                    Icons.person_outline,
                                    size: 32,
                                    color: AppColors.primary,
                                  )
                                : null,
                          ),
                          const SizedBox(width: 16),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  user.nickname,
                                  style: const TextStyle(
                                    fontSize: 22,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                                const SizedBox(height: 4),
                                Text(
                                  'ID：${user.userId}',
                                  style: const TextStyle(
                                    color: AppColors.secondary,
                                  ),
                                ),
                              ],
                            ),
                          ),
                          IconButton(
                            tooltip: '复制我的 ID',
                            onPressed: () async {
                              await Clipboard.setData(
                                ClipboardData(text: user.userId),
                              );
                              if (context.mounted) {
                                ScaffoldMessenger.of(context).showSnackBar(
                                  const SnackBar(content: Text('ID 已复制')),
                                );
                              }
                            },
                            icon: const Icon(Icons.copy_outlined, size: 20),
                          ),
                        ],
                      ),
                      loading: () =>
                          const Center(child: CircularProgressIndicator()),
                      error: (_, _) => Row(
                        children: [
                          const Expanded(child: Text('暂时无法读取个人资料')),
                          TextButton(
                            onPressed: () =>
                                ref.invalidate(mineProfileProvider),
                            child: const Text('重试'),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 20),
                Card(
                  color: Colors.white,
                  elevation: 0,
                  child: Column(
                    children: [
                      ListTile(
                        leading: const Icon(Icons.person_outline),
                        title: const Text('个人资料'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/mine/profile'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(leading: const Icon(Icons.qr_code),
                        title: const Text('我的二维码'), trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/qr/me')),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.notifications_outlined),
                        title: const Text('消息与隐私'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/mine/settings'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.lock_outline),
                        title: const Text('屏幕锁设置'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/mine/app-lock'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.lock_outline),
                        title: const Text('修改密码'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/mine/password'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.person_off_outlined),
                        title: const Text('注销账号'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/mine/cancel'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.feedback_outlined),
                        title: const Text('我的投诉'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/mine/complaints'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.bookmark_outline),
                        title: const Text('收藏'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/favorites'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.photo_library_outlined),
                        title: const Text('朋友圈'),
                        trailing: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            if ((momentUnread.value ?? 0) > 0)
                              Badge(label: Text('${momentUnread.value}')),
                            const Icon(Icons.chevron_right),
                          ],
                        ),
                        onTap: () async {
                          await context.push('/moments');
                          ref.invalidate(momentUnreadProvider);
                        },
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.edit_note_outlined),
                        title: const Text('笔记'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/personal/notes'),
                      ),
                      const Divider(height: 1, indent: 56),
                      ListTile(
                        leading: const Icon(Icons.checklist_outlined),
                        title: const Text('待办'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => context.push('/personal/todos'),
                      ),
                    ],
                  ),
                ),
                if (credentials != null && credentials.domains.length > 1) ...[
                  const SizedBox(height: 20),
                  Card(
                    color: Colors.white,
                    elevation: 0,
                    child: ListTile(
                      leading: const Icon(Icons.wifi_outlined),
                      title: const Text('连接线路'),
                      subtitle: Text('当前线路 ${credentials.lineIndex + 1}'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () async {
                        final index = await showModalBottomSheet<int>(
                          context: context,
                          builder: (context) => SafeArea(
                            child: Column(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                for (
                                  var i = 0;
                                  i < credentials.domains.length;
                                  i++
                                )
                                  ListTile(
                                    title: Text(i == 0 ? '主线路' : '备用线路 $i'),
                                    trailing: i == credentials.lineIndex
                                        ? const Icon(Icons.check)
                                        : null,
                                    onTap: () => Navigator.pop(context, i),
                                  ),
                              ],
                            ),
                          ),
                        );
                        if (index == null) return;
                        try {
                          await runtime.session.switchLine(index);
                        } on AppFailure catch (failure) {
                          if (context.mounted) {
                            ScaffoldMessenger.of(context).showSnackBar(
                              SnackBar(content: Text(failure.message)),
                            );
                          }
                        }
                      },
                    ),
                  ),
                ],
                const SizedBox(height: 28),
                OutlinedButton(
                  onPressed: () async {
                    final confirmed = await showDialog<bool>(
                      context: context,
                      builder: (context) => AlertDialog(
                        title: const Text('退出登录？'),
                        content: const Text('此账号的本地资料会保留，重新登录后可以继续使用。'),
                        actions: [
                          TextButton(
                            onPressed: () => Navigator.pop(context, false),
                            child: const Text('取消'),
                          ),
                          FilledButton(
                            onPressed: () => Navigator.pop(context, true),
                            child: const Text('退出'),
                          ),
                        ],
                      ),
                    );
                    if (confirmed != true) return;
                    try {
                      await runtime.session.signOut();
                    } on AppFailure catch (failure) {
                      if (context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(content: Text(failure.message)),
                        );
                      }
                    }
                  },
                  child: const Text('退出登录'),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
