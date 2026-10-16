import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/data/contacts_repository.dart';
import '../../contacts/domain/contact.dart';
import '../../contacts/presentation/select_contacts_page.dart';
import '../data/group_repository.dart';

/// An entry intent only. The accepted editor retains responsibility for its
/// explicit native dispatch, unknown-result freeze and read-only recovery.
class GroupCreationAction extends ConsumerStatefulWidget {
  const GroupCreationAction({
    super.key,
    this.peer,
    this.asListTile = false,
    this.checkVerification = false,
    this.sourceCurrent,
  });

  final Contact? peer;
  final bool asListTile;
  // Preserve the old inbox-only preflight. This is not server authorization;
  // the existing single-chat expansion did not apply this UI preflight.
  final bool checkVerification;
  final bool Function()? sourceCurrent;

  @override
  ConsumerState<GroupCreationAction> createState() => _GroupCreationActionState();
}

class _GroupCreationActionState extends ConsumerState<GroupCreationAction> {
  ContactsRepository? owner;
  GroupRepository? groups;
  TenantApi? api;
  late final Contact? peer;
  int view = 0;
  bool busy = false;

  @override
  void initState() {
    super.initState();
    peer = widget.peer;
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (ModalRoute.of(context)?.isCurrent == false) ++view;
  }

  @override
  void didUpdateWidget(covariant GroupCreationAction oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.peer?.id != widget.peer?.id ||
        oldWidget.checkVerification != widget.checkVerification) {
      ++view;
    }
  }

  bool get owned {
    final contacts = owner;
    final group = groups;
    final server = api;
    return mounted && contacts != null && group != null && server != null &&
      widget.peer?.id == peer?.id &&
      identical(contacts, ref.read(contactsRepositoryProvider)) &&
      identical(group, ref.read(groupRepositoryProvider)) &&
      identical(server, ref.read(tenantApiProvider)) &&
      contacts.access.isCurrent && group.access.isCurrent && server.access.isCurrent &&
      identical(contacts.access.credentials, contacts.access.session.state.credentials) &&
      identical(group.access.credentials, contacts.access.credentials) &&
      identical(server.access.credentials, contacts.access.credentials) &&
      widget.sourceCurrent?.call() != false;
  }

  Future<void> begin(int original) async {
    final route = ModalRoute.of(context);
    bool current() => owned && original == view && route?.isCurrent == true;
    if (busy || !current()) return;
    final contacts = owner!;
    final group = groups!;
    final server = api!;
    final selectedPeer = peer;
    final verification = widget.checkVerification;
    if (selectedPeer != null && (selectedPeer.id.isEmpty ||
        selectedPeer.id == contacts.access.credentials.userId)) {
      return;
    }
    setState(() => busy = true);
    try {
      if (verification) {
        // Read only this actor's existing status. Never retain, display or log
        // the endpoint's unrelated real-name/document fields.
        final raw = await server.chat.post('verification/status', {
          'userID': contacts.access.credentials.userId,
          'statusOnly': true,
        });
        if (!current()) return;
        if (raw is! Map || raw['status'] is! int ||
            !const [0, 1, 2, 3].contains(raw['status'])) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
        if (raw['status'] != 1) {
          final text = switch (raw['status']) {
            2 => '实名认证正在审核，请稍后重试。',
            3 => '实名认证未通过，请核对认证状态。',
            _ => '创建群聊前请先完成实名认证。',
          };
          if (mounted && current()) {
            ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
          }
          return;
        }
      }
      if (!mounted || !current()) return;
      await Navigator.of(context).push<void>(MaterialPageRoute(
        builder: (_) => SelectContactsPage(
          title: '创建群聊',
          createGroup: true,
          initialMembers: [?selectedPeer],
          expectedOwner: contacts,
          expectedGroups: group,
        ),
      ));
    } on AppFailure catch (error) {
      if (mounted && current()) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(
          verification ? '暂时无法确认认证状态，请重试。' : error.message,
        )));
      }
    } catch (_) {
      if (mounted && current()) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
          content: Text('暂时无法准备群聊，请重试。'),
        ));
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final contacts = ref.watch(contactsRepositoryProvider);
    final group = ref.watch(groupRepositoryProvider);
    final server = ref.watch(tenantApiProvider);
    if (contacts != null && group != null && server != null) {
      owner ??= contacts;
      groups ??= group;
      api ??= server;
    }
    final enabled = owned && !busy;
    final renderedView = view;
    if (widget.asListTile) {
      return ListTile(
        leading: busy ? const SizedBox.square(dimension: 20,
          child: CircularProgressIndicator(strokeWidth: 2)) : const Icon(Icons.group_add_outlined),
        title: Text(busy ? '正在准备群聊…' : '创建群聊'),
        subtitle: Text(owned ? '与当前联系人一起创建群聊' : '会话已变化，请重新打开'),
        onTap: enabled ? () => begin(renderedView) : null,
      );
    }
    return IconButton(
      tooltip: busy ? '正在检查创建群聊条件' : '创建群聊',
      constraints: const BoxConstraints(minWidth: 44, minHeight: 44),
      icon: busy ? const SizedBox.square(dimension: 20,
        child: CircularProgressIndicator(strokeWidth: 2)) : const Icon(Icons.group_add_outlined),
      onPressed: enabled ? () => begin(renderedView) : null,
    );
  }
}
