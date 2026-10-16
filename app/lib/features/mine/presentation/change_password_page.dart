import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../account_providers.dart';
import '../data/account_security_repository.dart';

class ChangePasswordPage extends ConsumerStatefulWidget {
  const ChangePasswordPage({super.key});
  @override
  ConsumerState<ChangePasswordPage> createState() => _ChangePasswordPageState();
}

class _ChangePasswordPageState extends ConsumerState<ChangePasswordPage> {
  final form = GlobalKey<FormState>();
  final current = TextEditingController(),
      next = TextEditingController(),
      confirm = TextEditingController();
  bool busy = false, terminal = false;
  final hidden = [true, true, true];
  String? failure;
  @override
  void dispose() {
    current.dispose();
    next.dispose();
    confirm.dispose();
    super.dispose();
  }

  Future<void> save() async {
    if (busy || terminal || form.currentState?.validate() != true) return;
    final repository = ref.read(passwordChangeRepositoryProvider);
    if (repository == null) return;
    final access = repository.access;
    final messenger = ScaffoldMessenger.of(context);
    final session = access.session;
    final origin = access.generation;
    setState(() {
      busy = true;
      failure = null;
    });
    try {
      final result = await repository.changePassword(current.text, next.text);
      if (mounted) {
        setState(() => terminal = true);
        current.clear();
        next.clear();
        confirm.clear();
      }
      // The server can revoke our native socket before the successful HTTP
      // response arrives. Do not let an old form sign out a newer account.
      var localCleanupFailed = false;
      if (access.isCurrent) {
        try {
          await session.signOut(revoke: false);
        } catch (_) {
          localCleanupFailed = true;
        }
      }
      final state = session.state;
      final stillOwnsSignedOut =
          state.phase == SessionPhase.signedOut &&
          state.generation == origin + 1 &&
          state.credentials == null;
      if (stillOwnsSignedOut && messenger.mounted) {
        messenger.showSnackBar(
          SnackBar(
            duration: const Duration(seconds: 8),
            content: Text(
              (result == PasswordChangeOutcome.confirmed
                      ? '密码已修改，请使用新密码重新登录'
                      : '修改结果暂未确认，请尝试使用新密码登录；不要重复提交改密请求') +
                  (localCleanupFailed ? '；本机登录资料暂未能清除' : ''),
            ),
          ),
        );
      }
    } catch (error) {
      if (mounted && access.isCurrent) {
        setState(
          () => failure = error is AppFailure && error.code == 1002
              ? '原密码不正确，或当前账号无权修改'
              : error is AppFailure
              ? error.message
              : '修改未完成，请稍后重试',
        );
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !busy,
    child: Scaffold(
      appBar: AppBar(title: const Text('修改密码')),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 560),
            child: Form(
              key: form,
              child: ListView(
                padding: const EdgeInsets.all(20),
                children: [
                  const Text('修改后需要重新登录，其他已登录设备也会退出。'),
                  const SizedBox(height: 24),
                  for (var i = 0; i < 3; i++) ...[
                    TextFormField(
                      controller: [current, next, confirm][i],
                      enabled: !busy && !terminal,
                      obscureText: hidden[i],
                      autocorrect: false,
                      enableSuggestions: false,
                      keyboardType: TextInputType.visiblePassword,
                      autofillHints: [
                        i == 0
                            ? AutofillHints.password
                            : AutofillHints.newPassword,
                      ],
                      decoration: InputDecoration(
                        labelText: ['原密码', '新密码（8～128 位）', '确认新密码'][i],
                        suffixIcon: IconButton(
                          tooltip: hidden[i] ? '显示密码' : '隐藏密码',
                          onPressed: busy
                              ? null
                              : () => setState(() => hidden[i] = !hidden[i]),
                          icon: Icon(
                            hidden[i]
                                ? Icons.visibility_outlined
                                : Icons.visibility_off_outlined,
                          ),
                        ),
                      ),
                      validator: (value) {
                        if (value == null || value.isEmpty) {
                          return '请填写${['原密码', '新密码', '确认密码'][i]}';
                        }
                        if (i == 1 &&
                            (value.length < 8 || value.length > 128)) {
                          return '新密码需要 8～128 位';
                        }
                        if (i == 1 && value == current.text) {
                          return '新密码不能与原密码相同';
                        }
                        if (i == 2 && value != next.text) return '两次新密码不一致';
                        return null;
                      },
                    ),
                    const SizedBox(height: 16),
                  ],
                  if (failure != null)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 16),
                      child: Text(
                        failure!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                  FilledButton(
                    onPressed: busy || terminal ? null : save,
                    child: Text(busy ? '正在修改…' : '确认修改'),
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
