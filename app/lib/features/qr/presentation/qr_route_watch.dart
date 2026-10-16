import 'package:flutter/material.dart';

/// An owned confirmation dialog can be covered by a foreign route. A normal
/// pop makes it inactive; a still-active non-current route retires its intent.
class QrRouteWatch extends StatefulWidget {
  const QrRouteWatch({super.key, required this.onCovered, required this.child});
  final VoidCallback onCovered;
  final Widget child;
  @override
  State<QrRouteWatch> createState() => _QrRouteWatchState();
}
class _QrRouteWatchState extends State<QrRouteWatch> {
  bool covered = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (!covered && route?.isActive == true && route?.isCurrent == false) {
      covered = true;
      widget.onCovered();
    }
  }
  @override
  Widget build(BuildContext context) => widget.child;
}
