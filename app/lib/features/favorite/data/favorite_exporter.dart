import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:path_provider/path_provider.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/privacy/message_privacy.dart';
import '../domain/favorite.dart';
import 'favorite_repository.dart';

enum FavoriteExportFormat {
  json('JSON', 'application/json'),
  csv('CSV', 'text/csv'),
  txt('文本', 'text/plain');

  const FavoriteExportFormat(this.label, this.mime);
  final String label, mime;
}

final class FavoriteExportFile {
  const FavoriteExportFile(this.path, this.format, this.count, {this.validate});
  final void Function()? validate;
  void check() => validate?.call();
  Future<void> discard() async {
    final file = File(path);
    if (await file.exists()) await file.delete();
  }

  final String path;
  final FavoriteExportFormat format;
  final int count;
}

final class FavoriteExporter {
  FavoriteExporter(this.repository, {Future<Directory> Function()? directory})
    : directory = directory ?? getTemporaryDirectory;
  final FavoriteRepository repository;
  final Future<Directory> Function() directory;

  Future<File> _newFile(String prefix, String extension, DateTime at) async {
    final root = await directory();
    repository.check();
    final folder = Directory(
      '${root.path}/favorites/${repository.api.access.credentials.accountKey}',
    );
    await folder.create(recursive: true);
    repository.check();
    final nonce = List.generate(
      8,
      (_) => Random.secure().nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
    return File(
      '${folder.path}/$prefix-${at.millisecondsSinceEpoch}-$nonce.$extension',
    );
  }

  Future<FavoriteExportFile> exportStats() async {
    repository.check();
    File? file;
    try {
      final stats = await repository.stats();
      final capturedAt = DateTime.now().toUtc();
      file = await _newFile('luobo-favorite-stats', 'json', capturedAt);
      await file.writeAsString(
        jsonEncode({
          'format': 'luobo-favorite-stats',
          'version': 1,
          'accountKey': repository.api.access.credentials.accountKey,
          'capturedAt': capturedAt.toIso8601String(),
          'periodTimezone': 'UTC',
          'weekStartsOn': 'Monday',
          'stats': stats.values,
        }),
        flush: true,
      );
      repository.check();
      return FavoriteExportFile(
        file.path,
        FavoriteExportFormat.json,
        stats.total,
        validate: repository.check,
      );
    } catch (error) {
      try {
        if (file != null && await file.exists()) await file.delete();
      } catch (_) {}
      if (error is AppFailure) rethrow;
      throw const AppFailure(FailureKind.localStorage);
    }
  }

  Future<FavoriteExportFile> export({
    FavoriteExportFormat format = FavoriteExportFormat.json,
    FavoriteQuery query = const FavoriteQuery(),
  }) async {
    repository.check();
    query = FavoriteQuery(
      keyword: query.keyword,
      type: query.type,
      categoryId: query.categoryId,
      tags: List.unmodifiable(query.tags),
    );
    final policies = <MessagePrivacy>[];
    void checkSources() {
      repository.check();
      if (policies.any((policy) => !policy.allowsRetention)) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
    }

    final capturedFrom = DateTime.now().toUtc();
    File? file;
    IOSink? sink;
    try {
      final categories = await repository.categories();
      final stats = await repository.stats();
      file = await _newFile('luobo-favorites', format.name, capturedFrom);
      sink = file.openWrite(encoding: utf8);
      // Attach an error handler before a slow network page can let sink errors
      // escape the awaited flush/close boundary.
      final completion = sink.done;
      completion.ignore();
      final byId = {
        for (final category in categories) category.id: category.name,
      };
      final header = {
        'format': 'luobo-favorites',
        'version': 1,
        'accountKey': repository.api.access.credentials.accountKey,
        'capturedFrom': capturedFrom.toIso8601String(),
        'query': query.toJson(),
        'categories': categories.map((category) => category.toJson()).toList(),
        'stats': stats.values,
      };
      switch (format) {
        case FavoriteExportFormat.json:
          final encodedHeader = jsonEncode(header);
          sink.write(
            '${encodedHeader.substring(0, encodedHeader.length - 1)},"favorites":[',
          );
        case FavoriteExportFormat.csv:
          sink.write(
            '\uFEFF${_row(['收藏ID', '类型', '标题', '内容', '分类', '标签', '备注', '创建时间', '更新时间', '来源ID', '会话ID'])}\r\n',
          );
        case FavoriteExportFormat.txt:
          sink.writeln('萝卜对话 · 收藏导出\n开始：${capturedFrom.toIso8601String()}\n');
      }
      final seen = <String>{};
      int? expectedTotal;
      var count = 0, page = 1;
      while (true) {
        final result = await repository.list(
          query: query,
          page: page,
          count: 100,
        );
        expectedTotal ??= result.total;
        if (expectedTotal != result.total) {
          throw const AppFailure(FailureKind.dataChanged);
        }
        for (final item in result.items) {
          if (!item.canUseContent) {
            throw const AppFailure(FailureKind.permissionDenied);
          }
          policies.add(item.decoded.privacy);
          if (!seen.add(item.id)) {
            throw const AppFailure(FailureKind.dataChanged);
          }
          repository.check();
          switch (format) {
            case FavoriteExportFormat.json:
              if (count > 0) sink.write(',');
              sink.write(jsonEncode(item.toJson()));
            case FavoriteExportFormat.csv:
              sink.write(
                '${_row([item.id, item.kind.label, item.title, item.content, byId[item.categoryId] ?? item.categoryId, jsonEncode(item.tags), item.notes, _time(item.createdAt), _time(item.updatedAt), item.sourceId, item.conversationId])}\r\n',
              );
            case FavoriteExportFormat.txt:
              sink.writeln(
                '${count + 1}. ${item.displayTitle}\n类型：${item.kind.label}\n分类：${byId[item.categoryId] ?? item.categoryId}\n标签：${item.tags.join('、')}\n内容：${item.content}\n备注：${item.notes}\n创建：${_time(item.createdAt)}\n',
              );
          }
          count++;
        }
        await sink.flush();
        checkSources();
        if (!result.hasMore) break;
        page++;
      }
      if (count != expectedTotal) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      if (format == FavoriteExportFormat.json) {
        sink.write(
          '],"exportedCount":$count,"capturedTo":${jsonEncode(DateTime.now().toUtc().toIso8601String())}}',
        );
      }
      await sink.flush();
      checkSources();
      await sink.close();
      sink = null;
      checkSources();
      return FavoriteExportFile(
        file.path,
        format,
        count,
        validate: checkSources,
      );
    } catch (error) {
      try {
        await sink?.close();
      } catch (_) {}
      try {
        if (file != null && await file.exists()) await file.delete();
      } catch (_) {}
      if (error is AppFailure) rethrow;
      throw const AppFailure(FailureKind.localStorage);
    }
  }

  String _time(int milliseconds) => DateTime.fromMillisecondsSinceEpoch(
    milliseconds,
    isUtc: true,
  ).toIso8601String();
  String _row(List<String> cells) => cells
      .map((cell) {
        final trimmed = cell.trimLeft();
        final safe =
            trimmed.isNotEmpty && '=+-@'.contains(trimmed[0]) ||
                cell.startsWith('\t') ||
                cell.startsWith('\r')
            ? "'$cell"
            : cell;
        return '"${safe.replaceAll('"', '""')}"';
      })
      .join(',');
}
