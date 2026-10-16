import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/tokens.dart';
import '../../contacts/data/contacts_repository.dart';
import '../../contacts/presentation/user_profile_page.dart';
import '../../group/data/group_repository.dart';
import '../domain/chat_card.dart';
import 'chat_content_access.dart';

/// Received FaceURL/ex never become image requests or links. Only an explicit
/// tap queries the existing, exact-user-ID SDK + business profile parser.
class ChatCardView extends StatefulWidget {
  const ChatCardView({
    super.key,
    required this.card,
    required this.captureAccess,
    required this.changes,
    required this.contacts,
    required this.contactsCurrent,
    this.sourceGroupId,
    this.sourceGroups,
  });
  final ChatCard? card;
  final ChatContentAccess Function(bool Function()) captureAccess;
  final Listenable changes;
  final ContactsRepository? contacts;
  final bool Function() contactsCurrent;
  final String? sourceGroupId;
  final GroupRepository? sourceGroups;
  @override
  State<ChatCardView> createState() => _ChatCardViewState();
}

class _ChatCardViewState extends State<ChatCardView>
    with WidgetsBindingObserver {
  ModalRoute<dynamic>? _origin;
  MaterialPageRoute<void>? _owned;
  int _turn = 0;
  bool _foreground = true;
  final _lifetime = StreamController<void>.broadcast();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  void _retire() {
    ++_turn;
    _lifetime.add(null);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _origin = ModalRoute.of(context);
    if (_origin?.isCurrent != true && _owned?.isActive != true) {
      _retire();
    }
  }

  @override
  void didUpdateWidget(covariant ChatCardView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.card != widget.card ||
        !identical(oldWidget.contacts, widget.contacts) ||
        oldWidget.sourceGroupId != widget.sourceGroupId ||
        !identical(oldWidget.sourceGroups, widget.sourceGroups)) {
      _retire();
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (!mounted) {
      return;
    }
    setState(() {
      _foreground = state == AppLifecycleState.resumed;
      if (!_foreground) {
        _retire();
      }
    });
  }

  @override
  void dispose() {
    _retire();
    unawaited(_lifetime.close());
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final card = widget.card;
    if (card == null) {
      return const Text('名片资料暂不可用');
    }
    final contacts = widget.contacts;
    final owner = contacts?.access.credentials;
    final generation = contacts?.access.generation;
    final issuer = owner?.issuer, tenant = owner?.tenantId;
    final origin = _origin, navigator = _origin?.navigator;
    var turn = _turn;
    var consumed = false;
    MaterialPageRoute<void>? detail;
    bool current() =>
        mounted &&
        _foreground &&
        turn == _turn &&
        widget.contactsCurrent() &&
        (contacts == null ||
            contacts.access.isCurrent &&
                contacts.access.generation == generation &&
                identical(contacts.access.session.state.credentials, owner) &&
                owner?.issuer == issuer &&
                owner?.tenantId == tenant) &&
        origin?.isActive == true &&
        navigator?.mounted == true &&
        identical(origin?.navigator, navigator) &&
        (detail == null ? origin?.isCurrent == true : detail!.isActive);
    ChatContentAccess? captured;
    try {
      captured = widget.captureAccess(current);
    } on AppFailure {
      // Capture once at render; a retained callback never receives a new cut.
    }
    final access = captured;
    if (access == null || !access.canView) {
      return const Text('消息内容已不可用');
    }
    void open() {
      if (consumed ||
          !current() ||
          !access.canView ||
          contacts == null ||
          navigator == null) {
        return;
      }
      consumed = true;
      turn = ++_turn;
      final route = MaterialPageRoute<void>(
        builder: (_) => ChatCardDetail(
          card: card,
          contacts: contacts,
          access: access,
          changes: widget.changes,
          sourceChanges: _lifetime.stream,
          sourceGroupId: widget.sourceGroupId,
          sourceGroups: widget.sourceGroups,
        ),
      );
      detail = _owned = route;
      unawaited(
        navigator.push<void>(route).whenComplete(() {
          if (identical(_owned, route)) _owned = null;
        }),
      );
    }

    return Semantics(
      label: '查看好友名片，${card.name}，账号 ${card.userId}',
      button: true,
      child: InkWell(
        onTap: open,
        borderRadius: BorderRadius.circular(12),
        child: ConstrainedBox(
          constraints: const BoxConstraints(
            minWidth: 44,
            minHeight: 44,
            maxWidth: 260,
          ),
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Row(
                  children: [
                    CircleAvatar(
                      backgroundColor: AppColors.primary.withValues(alpha: .1),
                      child: Text(String.fromCharCode(card.name.runes.first)),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        card.name,
                        maxLines: 3,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text('账号 ID：${card.userId}'),
                const SizedBox(height: 6),
                const Text(
                  '好友名片 · 查看资料',
                  style: TextStyle(color: AppColors.secondary, fontSize: 12),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class ChatCardDetail extends StatefulWidget {
  const ChatCardDetail({
    super.key,
    required this.card,
    required this.contacts,
    required this.access,
    required this.changes,
    required this.sourceChanges,
    this.sourceGroupId,
    this.sourceGroups,
  });
  final ChatCard card;
  final ContactsRepository contacts;
  final ChatContentAccess access;
  final Listenable changes;
  final Stream<void> sourceChanges;
  final String? sourceGroupId;
  final GroupRepository? sourceGroups;
  @override
  State<ChatCardDetail> createState() => _ChatCardDetailState();
}

class _ChatCardDetailState extends State<ChatCardDetail> {
  late final UserProfileSource _source;
  @override
  void initState() {
    super.initState();
    final original = widget;
    _source = UserProfileSource(
      repository: original.contacts,
      userId: original.card.userId,
      current: () => original.access.canView,
      canRetain: () => original.access.canRetain,
      changes: original.sourceChanges,
      uiChanges: original.changes,
      expiresAtMillis: () => original.access.privacy.expiresAtMilliseconds,
      now: original.access.repository.now,
      groupId: original.sourceGroupId,
      groupsRepository: original.sourceGroups,
    );
  }

  @override
  Widget build(BuildContext context) => UserProfilePage(
    userId: _source.userId,
    source: _source,
    groupId: _source.groupId,
  );
}
