final class GroupSummary {
  const GroupSummary({
    required this.id,
    required this.name,
    required this.ownerId,
    required this.members,
    required this.sessionType,
    this.avatar,
    this.status = 0,
  });
  final String id;
  final String name;
  final String ownerId;
  final int members;
  final int sessionType;
  final Uri? avatar;
  final int status;
}

final class GroupMember {
  const GroupMember({
    required this.id,
    required this.name,
    required this.role,
    required this.mutedUntil,
    this.avatar,
  });
  final String id;
  final String name;
  final int role;
  final int mutedUntil;
  final Uri? avatar;
  bool get owner => role == 100;
  bool get admin => role == 60;
  bool get muted => mutedUntil > DateTime.now().millisecondsSinceEpoch ~/ 1000;
  bool canModerate(GroupMember other) =>
      id != other.id &&
      ((owner && !other.owner) || (admin && other.role == 20));
}

final class GroupProfile {
  const GroupProfile({
    required this.summary,
    required this.joined,
    required this.me,
    required this.announcement,
    this.announcementUpdatedAt = 0,
    this.announcementPublisherId = '',
    required this.introduction,
    required this.verification,
    required this.hideMemberProfiles,
    required this.preventMemberFriends,
  });
  final GroupSummary summary;
  final bool joined;
  final GroupMember? me;
  final String announcement;
  final int announcementUpdatedAt;
  final String announcementPublisherId;
  final String introduction;
  final int verification;
  final bool hideMemberProfiles;
  final bool preventMemberFriends;
  bool get canManage => joined && (me?.owner == true || me?.admin == true);
}
