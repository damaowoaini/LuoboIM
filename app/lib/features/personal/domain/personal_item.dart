import 'dart:convert';
import 'dart:math';

import '../../../core/error/app_failure.dart';

enum PersonalKind { note, todo }

final class PersonalItem {
  const PersonalItem({
    required this.id,
    required this.kind,
    required this.title,
    required this.body,
    required this.createdAt,
    required this.updatedAt,
    this.category = '',
    this.pinned = false,
    this.completed = false,
    this.priority = 1,
    this.dueAt,
  });

  factory PersonalItem.create(PersonalKind kind) {
    final now = DateTime.now().toUtc();
    final random = Random.secure();
    return PersonalItem(
      id: List.generate(
        16,
        (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
      ).join(),
      kind: kind,
      title: '',
      body: '',
      createdAt: now,
      updatedAt: now,
    );
  }

  final String id;
  final PersonalKind kind;
  final String title;
  final String body;
  final String category;
  final DateTime createdAt;
  final DateTime updatedAt;
  final bool pinned;
  final bool completed;
  final int priority;
  final DateTime? dueAt;

  PersonalItem copyWith({
    String? id,
    String? title,
    String? body,
    String? category,
    bool? pinned,
    bool? completed,
    int? priority,
    DateTime? dueAt,
    bool clearDue = false,
  }) => PersonalItem(
    id: id ?? this.id,
    kind: kind,
    title: title ?? this.title,
    body: body ?? this.body,
    category: category ?? this.category,
    pinned: pinned ?? this.pinned,
    completed: completed ?? this.completed,
    priority: priority ?? this.priority,
    dueAt: clearDue ? null : dueAt ?? this.dueAt,
    createdAt: createdAt,
    updatedAt: DateTime.now().toUtc(),
  );

  Map<String, Object?> toJson() => {
    'id': id,
    'title': title,
    if (kind == PersonalKind.note) 'content': body else 'description': body,
    'createdAt': createdAt.toUtc().toIso8601String(),
    'updatedAt': updatedAt.toUtc().toIso8601String(),
    'category': category,
    if (kind == PersonalKind.note) 'isPinned': pinned,
    if (kind == PersonalKind.todo) ...{
      'isCompleted': completed,
      'priority': priority,
      'dueDate': dueAt?.toUtc().toIso8601String(),
    },
  };

  factory PersonalItem.fromJson(PersonalKind kind, Object? value) {
    const invalid = AppFailure(FailureKind.invalidInput);
    if (value is! Map) throw invalid;
    String text(String key, int max, {bool optional = false}) {
      final raw = value[key];
      if (raw == null && optional) return '';
      if (raw is! String || raw.length > max) throw invalid;
      return raw;
    }

    DateTime date(String key, {DateTime? fallback}) {
      final raw = value[key];
      if (raw == null && fallback != null) return fallback;
      final parsed = raw is String ? DateTime.tryParse(raw) : null;
      if (parsed == null || parsed.year < 1970 || parsed.year > 9999) {
        throw invalid;
      }
      return parsed.toUtc();
    }

    bool boolean(String key) {
      if (value[key] == null) return false;
      if (value[key] is! bool) throw invalid;
      return value[key] as bool;
    }

    final id = text('id', 256);
    if (id.isEmpty) throw invalid;
    final created = date('createdAt');
    final priority = value['priority'] ?? 1;
    if (priority is! int || priority < 0 || priority > 2) throw invalid;
    return PersonalItem(
      id: id,
      kind: kind,
      title: text('title', 1000),
      body: text(
        kind == PersonalKind.note ? 'content' : 'description',
        1000000,
        optional: kind == PersonalKind.todo,
      ),
      category: text('category', 200, optional: true),
      createdAt: created,
      updatedAt: date('updatedAt', fallback: created),
      pinned: boolean('isPinned'),
      completed: boolean('isCompleted'),
      priority: priority,
      dueAt: value['dueDate'] == null ? null : date('dueDate'),
    );
  }

  String get encoded => jsonEncode(toJson());
}
