import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../account_providers.dart';
import '../data/account_security_repository.dart';

class CancelAccountPage extends ConsumerStatefulWidget {
  const CancelAccountPage({super.key});

  @override
  ConsumerState<CancelAccountPage> createState() => _CancelAccountPageState();
}

class _CancelAccountPageState extends ConsumerState<CancelAccountPage> {
  final form = GlobalKey<FormState>();
  final password = TextEditingController(), code = TextEditingController();
  bool understood = false, hidden = true, busy = false, terminal = false;
  String? failure;

  @override
  void dispose() {
    password.dispose();
    code.dispose();
    super.dispose();
  }

  Future<void> cancelAccount() async {
    if (busy ||
        terminal ||
        !understood ||
        form.currentState?.validate() != true) {
      return;
    }
    final repository = ref.read(accountSecurityRepositoryProvider);
    if (repository == null) return;
    final access = repository.access;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('确认注销当前账号？'),
        content: const Text('提交后账号将停用，所有设备需要退出，无法继续登录。历史数据不会立即删除。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('返回'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
            child: const Text('确认注销'),
          ),
        ],
      ),
    );
    if (confirmed != true ||
        !mounted ||
        !access.isCurrent ||
        busy ||
        terminal) {
      return;
    }
    final messenger = ScaffoldMessenger.of(context);
    final session = access.session;
    final origin = access.generation;
    setState(() {
      busy = true;
      failure = null;
    });
    try {
      final result = await repository.cancelAccount(
        password.text,
        googleAuthCode: code.text.trim(),
      );
      if (mounted) {
        setState(() => terminal = true);
        password.clear();
        code.clear();
      }
      var localCleanupFailed = false;
      if (access.isCurrent) {
        try {
          await session.signOut(revoke: false);
        } catch (_) {
          localCleanupFailed = true;
        }
      }
      final state = session.state;
      if (state.phase == SessionPhase.signedOut &&
          state.generation == origin + 1 &&
          state.credentials == null &&
          messenger.mounted) {
        final message = switch (result) {
          AccountCancellationOutcome.sessionsRevoked =>
            '账号已停用，所有设备已退出。历史数据未立即删除。',
          AccountCancellationOutcome.revocationPending =>
            '注销请求已受理，账号已停用，其他设备退出正在处理中。历史数据未立即删除。',
          AccountCancellationOutcome.unconfirmed =>
            '注销结果未确认，当前设备已退出；请稍后确认账号状态，避免重复提交。',
        };
        messenger.showSnackBar(
          SnackBar(
            duration: const Duration(seconds: 10),
            content: Text(
              message + (localCleanupFailed ? ' 本机登录资料暂未能清除。' : ''),
            ),
          ),
        );
      }
    } catch (error) {
      if (mounted && access.isCurrent) {
        setState(() {
          failure = error is AppFailure && error.code == 1002
              ? '当前密码或动态验证码不正确，或当前账号无权注销'
              : error is AppFailure
              ? error.message
              : '注销请求未完成，请稍后重试';
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
      appBar: AppBar(title: const Text('注销账号')),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 560),
            child: Form(
              key: form,
              child: ListView(
                padding: const EdgeInsets.all(20),
                children: [
                  const Text('请先确认已保存需要保留的内容。'),
                  const SizedBox(height: 12),
                  const Text(
                    '注销后账号将停用，所有设备将退出且无法继续登录。当前流程不会立即删除历史消息和其他已保留的数据。',
                  ),
                  const SizedBox(height: 24),
                  TextFormField(
                    controller: password,
                    enabled: !busy && !terminal,
                    obscureText: hidden,
                    autocorrect: false,
                    enableSuggestions: false,
                    keyboardType: TextInputType.visiblePassword,
                    decoration: InputDecoration(
                      labelText: '当前密码',
                      suffixIcon: IconButton(
                        tooltip: hidden ? '显示密码' : '隐藏密码',
                        onPressed: busy || terminal
                            ? null
                            : () => setState(() => hidden = !hidden),
                        icon: Icon(
                          hidden
                              ? Icons.visibility_outlined
                              : Icons.visibility_off_outlined,
                        ),
                      ),
                    ),
                    validator: (value) =>
                        value == null || value.isEmpty ? '请填写当前密码' : null,
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: code,
                    enabled: !busy && !terminal,
                    keyboardType: TextInputType.number,
                    autocorrect: false,
                    enableSuggestions: false,
                    decoration: const InputDecoration(
                      labelText: '动态验证码（已启用时必填）',
                      helperText: '如未启用谷歌验证器，可以留空。',
                    ),
                    validator: (value) =>
                        value != null &&
                            value.trim().isNotEmpty &&
                            !RegExp(r'^\d{6}$').hasMatch(value.trim())
                        ? '请填写 6 位动态验证码'
                        : null,
                  ),
                  const SizedBox(height: 16),
                  CheckboxListTile(
                    value: understood,
                    onChanged: busy || terminal
                        ? null
                        : (value) =>
                              setState(() => understood = value ?? false),
                    contentPadding: EdgeInsets.zero,
                    controlAffinity: ListTileControlAffinity.leading,
                    title: const Text('我理解账号将停用，历史数据不会立即删除'),
                  ),
                  if (failure != null)
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 16),
                      child: Text(
                        failure!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                  const SizedBox(height: 16),
                  FilledButton(
                    onPressed: busy || terminal || !understood
                        ? null
                        : cancelAccount,
                    style: FilledButton.styleFrom(
                      backgroundColor: Theme.of(context).colorScheme.error,
                    ),
                    child: Text(busy ? '正在提交…' : '申请注销'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    ),
  );
}
