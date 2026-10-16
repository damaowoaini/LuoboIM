String meetingDate(DateTime date) {
  final local = date.toLocal();
  String two(int value) => '$value'.padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)} ${two(local.hour)}:${two(local.minute)}';
}
