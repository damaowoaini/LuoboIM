import 'package:flutter/material.dart';

import '../../../design/components/feedback.dart';
import '../../../design/tokens.dart';

String contactPath(String id, {String? groupId}) =>
    '/contacts/user/${Uri.encodeComponent(id)}${groupId == null ? '' : '?group=${Uri.encodeComponent(groupId)}'}';
String groupPath(String id) => '/contacts/groups/${Uri.encodeComponent(id)}';

class ContactAvatar extends StatelessWidget {
  const ContactAvatar({
    super.key,
    this.uri,
    this.group = false,
    this.radius = 23,
  });
  final Uri? uri;
  final bool group;
  final double radius;
  @override
  Widget build(BuildContext context) => CircleAvatar(
    radius: radius,
    backgroundColor: const Color(0xffdfeaff),
    backgroundImage: uri == null ? null : NetworkImage(uri.toString()),
    onBackgroundImageError: uri == null ? null : (_, _) {},
    child: uri == null
        ? Icon(
            group ? Icons.groups_outlined : Icons.person_outline,
            color: AppColors.primary,
          )
        : null,
  );
}

typedef ContactProblem = ErrorPanel;
final contactNotice = showNotice;
final contactConfirm = confirmAction;
final contactInput = promptText;
