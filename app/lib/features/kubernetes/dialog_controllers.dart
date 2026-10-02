import 'package:flutter/material.dart';

/// Shows a dialog whose fields use [controllers] and disposes them once the
/// dialog's exit animation has finished. Disposing as soon as the dialog's
/// result arrives would break the TextFields that are still animating out.
Future<T?> showDialogDisposing<T>(
  BuildContext context,
  List<ChangeNotifier> controllers,
  WidgetBuilder builder,
) async {
  final navigator = Navigator.of(context, rootNavigator: true);
  final route = DialogRoute<T>(
    context: context,
    builder: builder,
    themes: InheritedTheme.capture(from: context, to: navigator.context),
  );
  try {
    return await navigator.push(route);
  } finally {
    route.completed.whenComplete(() {
      for (final controller in controllers) {
        controller.dispose();
      }
    });
  }
}
