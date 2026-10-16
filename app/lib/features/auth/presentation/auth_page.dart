import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/app_runtime.dart';
import '../../../core/config/app_config.dart';
import '../../../core/error/app_failure.dart';
import '../../../design/components/brand_mark.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../data/auth_repository.dart';

enum AuthPageMode { login, register, reset }

final class _RegistrationLoginDraft {
  const _RegistrationLoginDraft(this.identity, this.password);
  final AuthIdentity identity;
  final String password;
}

class AuthPage extends ConsumerStatefulWidget {
  const AuthPage({super.key, this.mode = AuthPageMode.login});
  final AuthPageMode mode;
  @override
  ConsumerState<AuthPage> createState() => _AuthPageState();
}

class _AuthPageState extends ConsumerState<AuthPage> {
  final _form = GlobalKey<FormState>();
  final _account = TextEditingController();
  final _area = TextEditingController(text: '+86');
  final _password = TextEditingController();
  final _code = TextEditingController();
  final _google = TextEditingController();
  final _nickname = TextEditingController();
  final _invitation = TextEditingController();
  bool _codeLogin = false;
  bool _obscure = true;
  bool _busy = false;
  int _cooldown = 0;
  Timer? _timer;
  String? _error;
  _RegistrationLoginDraft? _unconfirmedRegistration;
  int _formEpoch = 0;
  bool get _editable => !_busy && _unconfirmedRegistration == null;
  bool _ownsForm(int epoch) =>
      mounted &&
      epoch == _formEpoch &&
      ModalRoute.of(context)?.isCurrent == true;
  void _returnToLogin() {
    _formEpoch++;
    context.go('/login');
  }

  bool get _isLogin => widget.mode == AuthPageMode.login;
  bool get _isRegister => widget.mode == AuthPageMode.register;
  AuthIdentity get _identity =>
      AuthIdentity(account: _account.text, areaCode: _area.text.trim());

  @override
  void dispose() {
    _timer?.cancel();
    for (final controller in [
      _account,
      _area,
      _password,
      _code,
      _google,
      _nickname,
      _invitation,
    ]) {
      controller.dispose();
    }
    super.dispose();
  }

  String? _required(String? value) =>
      value == null || value.trim().isEmpty ? '请填写此项' : null;

  Future<void> _sendCode() async {
    if (!_editable) return;
    final epoch = _formEpoch;
    if (_account.text.trim().isEmpty) {
      setState(() => _error = '请先填写手机号或邮箱');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final repo = await ref.read(appRuntimeProvider).auth();
      if (_isRegister) {
        final rules = await repo.registrationRules(
          invitationCode: _invitation.text.trim(),
        );
        if (rules.invitationRequired && _invitation.text.trim().isEmpty) {
          if (mounted) setState(() => _error = '请先填写邀请码');
          return;
        }
      }
      if (!_ownsForm(epoch)) return;
      setState(() => _cooldown = 60);
      _timer?.cancel();
      _timer = Timer.periodic(const Duration(seconds: 1), (timer) {
        if (!mounted) {
          timer.cancel();
          return;
        }
        setState(() => _cooldown--);
        if (_cooldown <= 0) timer.cancel();
      });
      await repo.sendCode(
        _identity,
        _isRegister
            ? VerificationPurpose.register
            : _isLogin
            ? VerificationPurpose.login
            : VerificationPurpose.resetPassword,
        invitationCode: _invitation.text.trim(),
      );
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('验证码已发送，请查收')));
      }
    } on AppFailure catch (error) {
      if (mounted) setState(() => _error = error.message);
    } catch (_) {
      if (mounted) setState(() => _error = '暂时无法发送，请稍后重试');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _submit() async {
    if (!_editable) return;
    if (!(_form.currentState?.validate() ?? false)) return;
    final epoch = _formEpoch;
    final identity = _identity;
    final password = _password.text;
    final nickname = _nickname.text;
    final code = _code.text.trim();
    final invitation = _invitation.text.trim();
    final google = _google.text.trim();
    final draft = _RegistrationLoginDraft(identity, password);
    var registrationAttempted = false;
    setState(() {
      _busy = true;
      _error = null;
    });
    final runtime = ref.read(appRuntimeProvider);
    try {
      if (_isLogin) {
        await runtime.signIn(
          identity: identity,
          password: _codeLogin ? null : password,
          verificationCode: _codeLogin ? code : null,
          googleAuthCode: google,
        );
      } else if (_isRegister) {
        final rules = await (await runtime.auth()).registrationRules(
          invitationCode: invitation,
        );
        if (!_ownsForm(epoch)) return;
        if (rules.invitationRequired && invitation.isEmpty) {
          if (mounted) setState(() => _error = '请填写邀请码');
          return;
        }
        if (rules.verificationRequired && code.isEmpty) {
          if (mounted) setState(() => _error = '请填写验证码');
          return;
        }
        registrationAttempted = true;
        await runtime.register(
          identity: identity,
          password: password,
          nickname: nickname,
          verifyCode: code,
          invitationCode: invitation,
        );
        if (mounted && runtime.phase != AppPhase.online) {
          _registrationFailed(draft, runtime.failure);
        }
      } else {
        await (await runtime.auth()).resetPassword(
          identity: identity,
          password: password,
          verifyCode: code,
        );
        if (mounted) {
          ScaffoldMessenger.of(context)
              .showSnackBar(const SnackBar(content: Text('密码已重置，请重新登录')));
          context.go('/login');
        }
      }
    } on AppFailure catch (error) {
      if (mounted) {
        if (registrationAttempted) {
          _registrationFailed(draft, error);
        } else {
          setState(() => _error = error.message);
        }
      }
    } catch (_) {
      if (mounted) {
        if (registrationAttempted) {
          _registrationFailed(draft, null);
        } else {
          setState(() => _error = '操作未完成，请稍后重试');
        }
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _registrationFailed(_RegistrationLoginDraft draft, AppFailure? failure) {
    // A timeout, pending response or failed auto-login may follow a committed
    // account and consumed OTP. Even a server argument error can come from a
    // later token/SDK step; it is not proof of rollback. Keep this view's draft
    // in memory and make the next action an explicit login, never a register.
    final cleanup = switch (failure?.cleanupWarning) {
      SessionCleanupWarning.pending => ' 本次自动登录会话正在撤销。',
      SessionCleanupWarning.unconfirmed => ' 本次自动登录会话的撤销尚未确认。',
      null => '',
    };
    setState(() {
      _unconfirmedRegistration = draft;
      _error = '注册结果尚未确认。请先使用原账号和密码登录，不要重复提交验证码。需要重新填写时可返回登录页。$cleanup';
    });
  }

  Future<void> _recoverRegistration() async {
    final draft = _unconfirmedRegistration;
    if (_busy || draft == null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    final runtime = ref.read(appRuntimeProvider);
    try {
      await runtime.signIn(identity: draft.identity, password: draft.password);
      if (mounted && runtime.phase != AppPhase.online) {
        setState(
          () => _error = '登录尚未完成。${runtime.failure?.message ?? '请稍后再尝试原密码登录'}',
        );
      }
    } catch (_) {
      if (mounted) setState(() => _error = '登录尚未完成，请稍后再尝试原密码登录');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final runtime = ref.watch(appRuntimeProvider);
    final title = _isLogin
        ? '欢迎回来'
        : _isRegister
        ? '创建账号'
        : '找回密码';
    return Scaffold(
      appBar: AppBar(
        leading: _isLogin ? null : BackButton(onPressed: _returnToLogin),
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 460),
            child: SingleChildScrollView(
              padding: const EdgeInsets.fromLTRB(24, 12, 24, 32),
              child: AutofillGroup(
                child: Form(
                  key: _form,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const BrandMark(),
                      const SizedBox(height: 24),
                      Text(
                        _isLogin ? Brand.name : title,
                        style: const TextStyle(
                          fontSize: 30,
                          fontWeight: FontWeight.w700,
                          color: AppColors.text,
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        _isLogin ? '让每一次对话，简单一点' : '连接朋友，分享生活',
                        style: const TextStyle(
                          color: AppColors.secondary,
                          fontSize: 16,
                        ),
                      ),
                      const SizedBox(height: 32),
                      TextFormField(
                        enabled: _editable,
                        controller: _account,
                        decoration: const InputDecoration(labelText: '手机号或邮箱'),
                        autofillHints: const [AutofillHints.username],
                        keyboardType: TextInputType.emailAddress,
                        validator: _required,
                        onChanged: (_) => setState(() {}),
                        textInputAction: TextInputAction.next,
                      ),
                      if (!_account.text.contains('@')) ...[
                        const SizedBox(height: 14),
                        TextFormField(
                          enabled: _editable,
                          controller: _area,
                          decoration: const InputDecoration(
                            labelText: '国家 / 地区代码',
                          ),
                          keyboardType: TextInputType.phone,
                          validator: (value) =>
                              RegExp(r'^\+[1-9][0-9]{0,3}$')
                                  .hasMatch(value?.trim() ?? '')
                              ? null
                              : '请填写有效区号，例如 +86',
                        ),
                      ],
                      if (_isRegister) ...[
                        const SizedBox(height: 14),
                        TextFormField(
                          enabled: _editable,
                          controller: _nickname,
                          decoration: const InputDecoration(labelText: '昵称'),
                          validator: _required,
                        ),
                        const SizedBox(height: 14),
                        TextFormField(
                          enabled: _editable,
                          controller: _invitation,
                          decoration: const InputDecoration(
                            labelText: '邀请码（如有）',
                          ),
                        ),
                      ],
                      if (!_isLogin || !_codeLogin) ...[
                        const SizedBox(height: 14),
                        TextFormField(
                          enabled: _editable,
                          controller: _password,
                          obscureText: _obscure,
                          autofillHints: [
                            _isLogin
                                ? AutofillHints.password
                                : AutofillHints.newPassword,
                          ],
                          decoration: InputDecoration(
                            labelText: _isLogin ? '密码' : '新密码（至少 8 位）',
                            suffixIcon: IconButton(
                              tooltip: _obscure ? '显示密码' : '隐藏密码',
                              onPressed: () =>
                                  setState(() => _obscure = !_obscure),
                              icon: Icon(
                                _obscure
                                    ? Icons.visibility_outlined
                                    : Icons.visibility_off_outlined,
                              ),
                            ),
                          ),
                          validator: (v) => _isLogin
                              ? _required(v)
                              : (v?.length ?? 0) < 8
                              ? '密码至少需要 8 位'
                              : null,
                        ),
                      ],
                      if (!_isLogin || _codeLogin) ...[
                        const SizedBox(height: 14),
                        Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Expanded(
                              child: TextFormField(
                                enabled: _editable,
                                controller: _code,
                                decoration: const InputDecoration(
                                  labelText: '验证码',
                                ),
                                autofillHints: const [
                                  AutofillHints.oneTimeCode,
                                ],
                                keyboardType: TextInputType.number,
                                validator: _isRegister ? null : _required,
                              ),
                            ),
                            const SizedBox(width: 10),
                            SizedBox(
                              height: 56,
                              child: OutlinedButton(
                                onPressed: !_editable || _cooldown > 0
                                    ? null
                                    : _sendCode,
                                child: Text(
                                  _cooldown > 0 ? '${_cooldown}s' : '获取验证码',
                                ),
                              ),
                            ),
                          ],
                        ),
                      ],
                      if (_isLogin) ...[
                        const SizedBox(height: 14),
                        ExpansionTile(
                          tilePadding: EdgeInsets.zero,
                          title: const Text(
                            '已绑定验证器？',
                            style: TextStyle(
                              fontSize: 14,
                              color: AppColors.secondary,
                            ),
                          ),
                          children: [
                            TextFormField(
                              enabled: _editable,
                              controller: _google,
                              decoration: const InputDecoration(
                                labelText: '验证器动态码',
                              ),
                              keyboardType: TextInputType.number,
                            ),
                          ],
                        ),
                      ],
                      const SizedBox(height: 16),
                      if (_error ?? runtime.failure?.message
                          case final String message)
                        Padding(
                          padding: const EdgeInsets.only(bottom: 16),
                          child: Semantics(
                            liveRegion: true,
                            child: Text(
                              message,
                              style: const TextStyle(color: AppColors.error),
                            ),
                          ),
                        ),
                      SizedBox(
                        width: double.infinity,
                        child: FilledButton(
                          onPressed:
                              _busy || runtime.phase == AppPhase.signingIn
                              ? null
                              : _unconfirmedRegistration != null
                              ? _recoverRegistration
                              : _submit,
                          child: _busy
                              ? const SizedBox(
                                  width: 22,
                                  height: 22,
                                  child: CircularProgressIndicator(
                                    strokeWidth: 2,
                                  ),
                                )
                              : Text(
                                  _unconfirmedRegistration != null
                                      ? '用原密码登录'
                                      : _isLogin
                                      ? '登录'
                                      : _isRegister
                                      ? '注册并登录'
                                      : '重置密码',
                                ),
                        ),
                      ),
                      if (_isLogin) ...[
                        const SizedBox(height: 12),
                        Wrap(
                          alignment: WrapAlignment.spaceBetween,
                          spacing: 16,
                          children: [
                            TextButton(
                              onPressed: _busy
                                  ? null
                                  : () => setState(
                                      () => _codeLogin = !_codeLogin,
                                    ),
                              child: Text(_codeLogin ? '使用密码登录' : '使用验证码登录'),
                            ),
                            TextButton(
                              onPressed: _busy
                                  ? null
                                  : () => context.go('/reset'),
                              child: const Text('忘记密码'),
                            ),
                          ],
                        ),
                        Center(
                          child: TextButton(
                            onPressed: _busy
                                ? null
                                : () => context.go('/register'),
                            child: const Text('还没有账号？立即注册'),
                          ),
                        ),
                        if (runtime.lines.length > 1)
                          Center(
                            child: TextButton.icon(
                              onPressed: _busy
                                  ? null
                                  : () => _chooseLine(runtime),
                              icon: const Icon(Icons.wifi_rounded, size: 18),
                              label: Text('当前线路 ${runtime.lineIndex + 1}'),
                            ),
                          ),
                      ],
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _chooseLine(AppRuntime runtime) => showModalBottomSheet<void>(
    context: context,
    builder: (sheetContext) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Padding(
            padding: EdgeInsets.all(20),
            child: Text(
              '选择连接线路',
              style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
            ),
          ),
          for (var i = 0; i < runtime.lines.length; i++)
            ListTile(
              title: Text(i == 0 ? '主线路' : '备用线路 $i'),
              trailing: runtime.lineIndex == i
                  ? const Icon(Icons.check, color: AppColors.primary)
                  : null,
              onTap: () {
                runtime.chooseRoute(i);
                Navigator.pop(sheetContext);
                setState(() {});
              },
            ),
        ],
      ),
    ),
  );
}
