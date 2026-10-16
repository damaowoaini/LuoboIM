enum ContactRequestKind { friend, group }

enum ContactLookupMode { userId, phoneNumber }

final class Contact {
  const Contact({
    required this.id,
    required this.name,
    this.remark = '',
    this.avatar,
    this.friend = false,
    this.blocked = false,
    this.canRequestFriend = true,
  });
  final String id;
  final String name;
  final String remark;
  final Uri? avatar;
  final bool friend;
  final bool blocked;
  final bool canRequestFriend;
  String get displayName => remark.trim().isNotEmpty
      ? remark
      : name.trim().isNotEmpty
      ? name
      : id;
}

final class ContactRequest {
  const ContactRequest({
    required this.kind,
    required this.userId,
    required this.name,
    required this.incoming,
    required this.status,
    required this.createdAt,
    this.groupId,
    this.groupName,
    this.inviterId,
    this.reason = '',
    this.avatar,
  });
  final ContactRequestKind kind;
  final String userId;
  final String name;
  final bool incoming;
  final int status;
  final int createdAt;
  final String? groupId;
  final String? groupName;
  final String? inviterId;
  final String reason;
  final Uri? avatar;
  bool get pending => status == 0;
  String get key =>
      '${kind.name}:${groupId ?? ''}:$userId:$incoming:$createdAt';
}

final class ContactSearchPage {
  const ContactSearchPage(this.items, this.total);
  final List<Contact> items;
  final int total;
}
