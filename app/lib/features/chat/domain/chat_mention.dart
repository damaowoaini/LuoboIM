/// Pinned OpenIM spelling; group SDK exposes the same atAllTag value.
const mentionAllId = 'AtAllTag';
const maximumMentionRecipients = 10;

final class MentionIdentity {
  const MentionIdentity(this.userId, this.name);
  final String userId, name;
  bool get all => userId == mentionAllId;
  String get label => '@${all ? '所有人' : name}';
  String get identityLabel => all ? '群内所有成员' : '账号 ID：$userId';
  bool get valid =>
      userId.isNotEmpty &&
      userId.length <= 256 &&
      !RegExp(r'[\s\x00-\x1f\x7f]').hasMatch(userId) &&
      name.isNotEmpty &&
      name.length <= 256 &&
      !RegExp(r'[\r\n\x00-\x1f\x7f]').hasMatch(name);
}

/// Offsets are UTF16 in the exact visible EditableText string, not wire text.
final class MentionToken {
  const MentionToken(this.start, this.end, this.identity);
  final int start, end;
  final MentionIdentity identity;
  MentionToken shifted(int delta) =>
      MentionToken(start + delta, end + delta, identity);
  bool matches(String text) =>
      identity.valid &&
      start >= 0 &&
      end > start &&
      end <= text.length &&
      text.substring(start, end) == identity.label;
}

final class MentionDraft {
  MentionDraft(this.text, Iterable<MentionToken> tokens)
    : tokens = List.unmodifiable(tokens);
  final String text;
  final List<MentionToken> tokens;
  bool get valid {
    var end = 0;
    for (final token in tokens) {
      if (!token.matches(text) || token.start < end) return false;
      end = token.end;
    }
    return recipients.length <= maximumMentionRecipients;
  }

  List<MentionIdentity> get recipients {
    final found = <String>{};
    return List.unmodifiable(
      tokens
          .where((token) => found.add(token.identity.userId))
          .map((token) => token.identity),
    );
  }

  String get wireText {
    if (!valid) throw const FormatException('invalid mention ranges');
    final result = StringBuffer();
    var cursor = 0;
    for (final token in tokens) {
      result.write(text.substring(cursor, token.start));
      result.write('@${token.identity.userId}');
      cursor = token.end;
    }
    result.write(text.substring(cursor));
    return result.toString();
  }

  // SDK drafts are untrusted user-visible plain text. A token binding is an
  // explicit selection in this editor only, never reconstructed from text or
  // JSON supplied by a user. No durable token sidecar is claimed here.
  String encode() => text;
  static MentionDraft restore(String stored) => MentionDraft(stored, const []);
}

/// Actual received @UID occurrences, paired only with protocol recipient IDs.
final class ReceivedMention {
  const ReceivedMention(this.start, this.end, this.identity);
  final int start, end;
  final MentionIdentity identity;
}
