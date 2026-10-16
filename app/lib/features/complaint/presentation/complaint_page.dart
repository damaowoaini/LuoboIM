import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/storage/object_uploader.dart';
import '../../../core/storage/staged_image.dart';
import '../../../design/components/feedback.dart';
import '../../../providers.dart';
import '../complaint_providers.dart';
import '../data/complaint_repository.dart';

final class _Evidence {
  _Evidence(this.local);
  final StagedImage local;
  Uri? uploaded;
}

class ComplaintPage extends ConsumerStatefulWidget {
  const ComplaintPage({
    super.key,
    required this.target,
    required this.targetId,
    required this.targetName,
  });
  final ComplaintTarget target;
  final String targetId, targetName;
  @override
  ConsumerState<ComplaintPage> createState() => _ComplaintPageState();
}

class _ComplaintPageState extends ConsumerState<ComplaintPage> {
  late final ComplaintRepository? owner = ref.read(complaintRepositoryProvider);
  final description = TextEditingController();
  final evidence = <_Evidence>[];
  final submissionId = ComplaintRepository.newSubmissionId();
  String? reason, failure;
  bool busy = false, committing = false, uncertain = false, allowPop = false;
  bool cancelRequested = false, checkingReceipt = false;
  double progress = 0;
  ObjectUploader? uploader;
  Future<String> Function()? pendingSubmission;
  bool get dirty =>
      reason != null || description.text.isNotEmpty || evidence.isNotEmpty;

  Future<void> leave() async {
    if (busy || !mounted) return;
    if (dirty &&
        !await confirmAction(
          context,
          '离开投诉编辑？',
          uncertain ? '提交结果尚未确认。请先查看我的投诉，避免重复提交。' : '尚未提交的编辑和所选图片将被清除。',
          action: '离开',
        )) {
      return;
    }
    if (!mounted) return;
    setState(() => allowPop = true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.pop(context);
    });
  }

  Future<void> pick() async {
    final repository = owner;
    if (repository == null ||
        !repository.isCurrent ||
        busy ||
        uncertain ||
        evidence.length >= 9) {
      return;
    }
    setState(() {
      busy = true;
      failure = null;
    });
    final prepared = <StagedImage>[];
    try {
      final remaining = 9 - evidence.length;
      final picked = await ImagePicker().pickMultiImage(
        limit: remaining,
        imageQuality: 85,
        maxWidth: 2560,
        maxHeight: 2560,
        requestFullMetadata: false,
      );
      repository.check();
      final stager = ImageStager(
        ref.read(imGatewayProvider),
        repository.api.access,
      );
      for (final image in picked.take(remaining)) {
        if (!mounted) throw const AppFailure(FailureKind.cancelled);
        prepared.add(await stager.prepare(image.path));
      }
      if (!mounted || !repository.isCurrent) return;
      setState(() => evidence.addAll(prepared.map(_Evidence.new)));
      prepared.clear();
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(
          () => failure = error is AppFailure
              ? error.message
              : '无法读取图片，请检查权限或选择其他图片',
        );
      }
    } finally {
      for (final image in prepared) {
        await image.dispose();
      }
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> submit({bool replay = false}) async {
    final repository = owner;
    if (repository == null ||
        busy ||
        !repository.isCurrent ||
        (uncertain && (!replay || !repository.canReplay))) {
      return;
    }
    if (reason == null) {
      showNotice(context, '请选择投诉原因');
      return;
    }
    final task = ObjectUploader(repository.api);
    uploader = task;
    setState(() {
      busy = true;
      failure = null;
      progress = 0;
      cancelRequested = false;
    });
    try {
      for (var i = 0; pendingSubmission == null && i < evidence.length; i++) {
        final item = evidence[i], index = i;
        item.uploaded ??= await task.upload(
          item.local.file,
          contentType: item.local.contentType,
          extension: item.local.extension,
          group: 'complaint',
          progress: (value) {
            if (mounted && repository.isCurrent) {
              setState(() => progress = (index + value) / evidence.length);
            }
          },
        );
        repository.check();
        if (cancelRequested || !mounted) {
          throw const AppFailure(FailureKind.cancelled);
        }
      }
      repository.check();
      if (cancelRequested || !mounted) {
        throw const AppFailure(FailureKind.cancelled);
      }
      setState(() => committing = true);
      if (pendingSubmission == null) {
        final target = widget.target;
        final targetId = widget.targetId, targetName = widget.targetName;
        final reasonSnapshot = reason!, descriptionSnapshot = description.text;
        final evidenceSnapshot = List<Uri>.unmodifiable(
          evidence.map((i) => i.uploaded!),
        );
        pendingSubmission = () => repository.submit(
          submissionId: submissionId,
          target: target,
          targetId: targetId,
          targetName: targetName,
          reason: reasonSnapshot,
          description: descriptionSnapshot,
          evidence: evidenceSnapshot,
        );
      }
      await pendingSubmission!();
      if (!mounted || !repository.isCurrent) return;
      showNotice(context, '投诉已提交，可在“我的投诉”查看处理状态');
      setState(() => allowPop = true);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && repository.isCurrent) Navigator.pop(context, true);
      });
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(() {
          uncertain =
              committing &&
              !(error is AppFailure &&
                  (error.code == 1001 ||
                      [
                        FailureKind.permissionDenied,
                        FailureKind.invalidInput,
                        FailureKind.sessionExpired,
                        FailureKind.rateLimited,
                      ].contains(error.kind)));
          failure = uncertain
              ? '暂未确认提交结果。当前编辑仍保留，请先查询本次提交结果。'
              : error is AppFailure
              ? error.message
              : '投诉未提交，请稍后重试';
          if (!uncertain) pendingSubmission = null;
        });
      }
    } finally {
      task.close();
      if (identical(task, uploader)) uploader = null;
      if (mounted) {
        setState(() {
          busy = false;
          committing = false;
        });
      }
    }
  }

  Future<void> retrySubmission() => submit(replay: true);

  Future<void> recoverReceipt() async {
    final repository = owner;
    if (repository == null || !repository.isCurrent || busy || !uncertain) {
      return;
    }
    setState(() {
      busy = true;
      checkingReceipt = true;
      failure = null;
    });
    try {
      final found = await repository.receipt(submissionId);
      if (!mounted || !repository.isCurrent) return;
      if (found == null) {
        setState(
          () => failure = repository.canReplay
              ? '尚未查到回执，原请求仍可能在处理中；可稍后查询，或重试本次提交。'
              : '尚未查到回执，原请求仍可能在处理中；请稍后再查，避免重复提交。',
        );
      } else {
        showNotice(context, '已确认投诉提交成功');
        setState(() => allowPop = true);
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted && repository.isCurrent) Navigator.pop(context, true);
        });
      }
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(
          () =>
              failure = error is AppFailure ? error.message : '暂时无法确认回执，请稍后再查',
        );
      }
    } finally {
      if (mounted) {
        setState(() {
          busy = false;
          checkingReceipt = false;
        });
      }
    }
  }

  @override
  void dispose() {
    description.dispose();
    uploader?.close();
    for (final item in evidence) {
      unawaited(item.local.dispose());
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final current = ref.watch(complaintRepositoryProvider);
    if (owner == null || !identical(owner, current) || !owner!.isCurrent) {
      return const Scaffold(
        body: SafeArea(child: Center(child: Text('账号已变化，请返回后重新进入'))),
      );
    }
    final enabled = !busy && !uncertain;
    return PopScope(
      canPop: allowPop,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) unawaited(leave());
      },
      child: Scaffold(
        appBar: AppBar(
          title: const Text('投诉'),
          leading: BackButton(onPressed: busy ? null : leave),
        ),
        body: SafeArea(
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 680),
              child: ListView(
                padding: const EdgeInsets.all(20),
                children: [
                  Text(
                    '投诉${widget.target == ComplaintTarget.user ? '用户' : '群组'}：${widget.targetName}',
                  ),
                  const SizedBox(height: 8),
                  SelectableText('ID：${widget.targetId}'),
                  const SizedBox(height: 20),
                  DropdownButtonFormField<String>(
                    initialValue: reason,
                    isExpanded: true,
                    decoration: const InputDecoration(labelText: '投诉原因'),
                    items:
                        (widget.target == ComplaintTarget.user
                                ? ComplaintRepository.userReasons
                                : ComplaintRepository.groupReasons)
                            .map(
                              (value) => DropdownMenuItem(
                                value: value,
                                child: Text(value),
                              ),
                            )
                            .toList(),
                    onChanged: enabled
                        ? (value) => setState(() => reason = value)
                        : null,
                  ),
                  const SizedBox(height: 20),
                  TextField(
                    controller: description,
                    enabled: enabled,
                    minLines: 3,
                    maxLines: 7,
                    maxLength: 1000,
                    decoration: const InputDecoration(
                      labelText: '补充说明（选填）',
                      hintText: '请说明具体情况',
                    ),
                  ),
                  const SizedBox(height: 12),
                  const Text('证据图片（最多 9 张，单张不超过 20MB）'),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 12,
                    runSpacing: 12,
                    children: [
                      for (final item in evidence)
                        SizedBox(
                          width: 104,
                          child: Column(
                            children: [
                              Image.file(
                                item.local.file,
                                height: 88,
                                width: 104,
                                fit: BoxFit.cover,
                                cacheWidth: 208,
                                errorBuilder: (_, _, _) => const SizedBox(
                                  height: 88,
                                  child: Icon(Icons.broken_image_outlined),
                                ),
                              ),
                              TextButton(
                                onPressed: enabled
                                    ? () {
                                        setState(() => evidence.remove(item));
                                        unawaited(item.local.dispose());
                                      }
                                    : null,
                                child: const Text('移除'),
                              ),
                            ],
                          ),
                        ),
                    ],
                  ),
                  if (evidence.length < 9)
                    OutlinedButton.icon(
                      onPressed: enabled ? pick : null,
                      icon: const Icon(Icons.add_photo_alternate_outlined),
                      label: const Text('选择证据图片'),
                    ),
                  if (busy)
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 16),
                      child: Column(
                        children: [
                          if (uploader != null &&
                              evidence.isNotEmpty &&
                              !committing)
                            LinearProgressIndicator(value: progress),
                          Text(
                            checkingReceipt
                                ? '正在查询提交结果…'
                                : committing
                                ? '正在提交…'
                                : '正在准备或上传图片…',
                          ),
                          if (uploader != null && !committing)
                            TextButton(
                              onPressed: () {
                                cancelRequested = true;
                                uploader?.close();
                              },
                              child: const Text('取消上传'),
                            ),
                        ],
                      ),
                    ),
                  if (failure != null)
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 16),
                      child: Text(
                        failure!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                  const SizedBox(height: 16),
                  FilledButton(
                    onPressed: enabled ? submit : null,
                    child: const Text('提交投诉'),
                  ),
                  if (uncertain)
                    OutlinedButton(
                      onPressed: busy ? null : recoverReceipt,
                      child: const Text('查询本次提交结果'),
                    ),
                  if (uncertain && owner!.canReplay)
                    OutlinedButton(
                      onPressed: busy ? null : retrySubmission,
                      child: const Text('重试本次提交'),
                    ),
                  TextButton(
                    onPressed: busy
                        ? null
                        : () => context.push('/mine/complaints'),
                    child: const Text('查看我的投诉'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
