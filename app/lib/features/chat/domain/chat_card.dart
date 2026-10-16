import 'dart:convert';
import 'dart:math';

/// A card is an untrusted identity hint, not a friendship or action grant.
/// FaceURL/ex remain protocol fields only; receiving never opens either.
final class ChatCard {
  const ChatCard._(this.userId, this.name);
  final String userId, name;

  static ChatCard? parse({
    required Object? userId,
    required Object? name,
    Object? faceUrl,
    Object? extra,
  }) {
    if (userId is! String ||
        userId.isEmpty ||
        userId.length > 128 ||
        userId != userId.trim() ||
        !_plain(userId) ||
        RegExp(r'\s').hasMatch(userId) ||
        (name != null && name is! String) ||
        (faceUrl != null && faceUrl is! String) ||
        (extra != null && extra is! String)) {
      return null;
    }
    final label = name as String? ?? '';
    if (label.runes.length > 128 ||
        !_plain(label) ||
        (faceUrl as String? ?? '').length > 2048 ||
        (extra as String? ?? '').length > 4096 ||
        [
              userId,
              label,
              faceUrl ?? '',
              extra ?? '',
            ].map((v) => utf8.encode(v).length).fold<int>(0, (a, b) => a + b) >
            8192) {
      return null;
    }
    return ChatCard._(userId, label.trim().isEmpty ? userId : label.trim());
  }

  static bool _plain(String value) =>
      utf8.decode(utf8.encode(value)) == value &&
      !RegExp(
        r'[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]',
      ).hasMatch(value);

  @override
  bool operator ==(Object other) =>
      other is ChatCard && other.userId == userId && other.name == name;
  @override
  int get hashCode => Object.hash(userId, name);
  @override
  String toString() => 'ChatCard';
}

String newCardIntent() {
  final random = Random.secure();
  return List.generate(
    16,
    (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
  ).join();
}

enum CardSendKind { sent, notDispatched, unconfirmed }

final class CardSendResult {
  const CardSendResult(this.kind, [this.messageId]);
  final CardSendKind kind;
  final String? messageId;
}
