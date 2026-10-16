import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/access/native_access.dart';
import '../../../providers.dart';

class AppLockSettingsPage extends ConsumerStatefulWidget {
  const AppLockSettingsPage({super.key});
  @override
  ConsumerState<AppLockSettingsPage> createState() =>
      _AppLockSettingsPageState();
}

class _AppLockSettingsPageState extends ConsumerState<AppLockSettingsPage> {
  String? notice;
  bool busy = false;
  Future<void> action(String kind) async {
    if (busy) return;
    final access = ref.read(appAccessProvider);
    final owner = access.owner, revision = access.revision;
    if (!access.unlocked || owner == null) return;
    final values = await showDialog<Map<String, String>>(
      context: context,
      builder: (_) => _PinDialog(
        kind: kind,
        enabled: access.enabled,
        biometric: access.biometricEnabled,
      ),
    );
    if (!mounted ||
        values == null ||
        !access.unlocked ||
        access.owner != owner ||
        access.revision != revision) {
      return;
    }
    setState(() {
      busy = true;
      notice = null;
    });
    try {
      if (kind == 'pin' || kind == 'recover') {
        if (values['pin'] != values['confirmation']) {
          setState(() => notice = '两次输入的解锁密码不一致');
          return;
        }
        await access.setPin(
          values['pin']!,
          currentPin: values['old'],
          resetAfterLogin: kind == 'recover',
        );
      } else if (kind == 'disable') {
        await access.disable(values['old']!);
      } else {
        await access.setBiometric(!access.biometricEnabled, values['old']!);
      }
      if (mounted && access.owner == owner && access.unlocked) {
        setState(() => notice = '屏幕锁设置已保存');
      }
    } catch (error) {
      if (mounted && access.owner == owner && access.unlocked) {
        setState(
          () =>
              notice = error is AccessFailure ? error.message : '设置尚未确认，请刷新核对',
        );
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final access = ref.watch(appAccessProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('屏幕锁设置')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          ListTile(
            leading: const Icon(Icons.lock_outline),
            title: Text(access.enabled ? '修改解锁密码' : '设置解锁密码'),
            subtitle: Text(
              access.enabled ? '已开启，返回应用时验证身份' : '6–12位数字，仅用于本机解锁',
            ),
            onTap: busy || access.busy ? null : () => action('pin'),
          ),
          if (access.enabled) ...[
            SwitchListTile(
              title: const Text('生物识别解锁'),
              subtitle: const Text('需先验证解锁密码，再验证此设备已登记的生物识别'),
              value: access.biometricEnabled,
              onChanged: busy || access.busy ? null : (_) => action('bio'),
            ),
            ListTile(
              title: const Text('立即锁定'),
              leading: const Icon(Icons.lock),
              onTap: busy || access.busy ? null : access.lock,
            ),
            ListTile(
              title: const Text('关闭屏幕锁'),
              onTap: busy || access.busy ? null : () => action('disable'),
            ),
            if (access.canResetAfterLogin)
              ListTile(
                title: const Text('使用此次登录验证重设解锁密码'),
                subtitle: const Text('已重新验证账号后可重设；已有生物识别需重新开启'),
                onTap: busy || access.busy ? null : () => action('recover'),
              ),
          ],
          const Padding(
            padding: EdgeInsets.all(12),
            child: Text(
              '开启后保护应用页面和后台预览；Android同时限制截图。锁定会关闭未提交的弹窗与编辑器，解锁后返回消息页，请先保存编辑内容。屏幕锁不会退出账号或自动结束已建立的通话，也不替代设备锁与数据备份保护。旧版解锁密码不会自动迁移。',
            ),
          ),
          if (busy || access.busy) const LinearProgressIndicator(),
          if (notice != null)
            Padding(padding: const EdgeInsets.all(12), child: Text(notice!)),
        ],
      ),
    );
  }
}

class _PinDialog extends StatefulWidget {
  const _PinDialog({
    required this.kind,
    required this.enabled,
    required this.biometric,
  });
  final String kind;
  final bool enabled, biometric;
  @override
  State<_PinDialog> createState() => _PinDialogState();
}

class _PinDialogState extends State<_PinDialog> {
  final old = TextEditingController(),
      pin = TextEditingController(),
      confirmation = TextEditingController();
  @override
  void dispose() {
    old.dispose();
    pin.dispose();
    confirmation.dispose();
    super.dispose();
  }

  Widget field(TextEditingController controller, String label) => TextField(
    controller: controller,
    obscureText: true,
    enableSuggestions: false,
    autocorrect: false,
    enableInteractiveSelection: false,
    maxLength: 12,
    keyboardType: TextInputType.number,
    inputFormatters: [FilteringTextInputFormatter.digitsOnly],
    decoration: InputDecoration(labelText: label),
  );
  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(switch (widget.kind) {
      'pin' => widget.enabled ? '修改解锁密码' : '设置解锁密码',
      'recover' => '使用此次登录验证重设',
      'disable' => '关闭屏幕锁',
      _ => '验证解锁密码',
    }),
    content: SingleChildScrollView(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (widget.enabled && widget.kind != 'recover') field(old, '当前解锁密码'),
          if (widget.kind == 'pin' || widget.kind == 'recover') ...[
            field(pin, '新解锁密码（6–12位数字）'),
            field(confirmation, '再次输入新解锁密码'),
          ],
          if (widget.kind == 'bio' && !widget.biometric)
            const Text('设备上已登记的生物识别均可能解锁此账号。本功能用于本机访问验证。'),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: () => Navigator.pop(context, {
          'old': old.text,
          'pin': pin.text,
          'confirmation': confirmation.text,
        }),
        child: const Text('确认'),
      ),
    ],
  );
}
