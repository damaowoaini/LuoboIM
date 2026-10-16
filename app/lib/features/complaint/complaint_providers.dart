import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/tenant_api.dart';
import 'data/complaint_repository.dart';

final complaintRepositoryProvider = Provider.autoDispose<ComplaintRepository?>((
  ref,
) {
  final api = ref.watch(tenantApiProvider);
  return api == null ? null : ComplaintRepository(api);
});
