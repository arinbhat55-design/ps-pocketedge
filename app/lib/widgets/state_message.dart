import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;

import '../api/api_client.dart';

/// Below this width, screens switch to their phone layout: bottom
/// navigation instead of a side rail, and cards instead of wide tables.
const double kCompactWidth = 600;

/// Below this width a multi-column [DataTable] can't fit without
/// horizontal scrolling, so list screens render compact cards instead.
const double kTableMinWidth = 720;

/// How often list screens (servers, containers) re-fetch in the
/// background, so state changes show up without a manual refresh.
const Duration kListPollInterval = Duration(seconds: 10);

bool isCompactWidth(BuildContext context) =>
    MediaQuery.sizeOf(context).width < kCompactWidth;

/// Turns a load failure into one readable sentence: the control plane's own
/// message for API errors, a connectivity hint for transport errors, and
/// the raw exception only as a last resort.
String describeLoadError(Object? error) {
  if (error is ApiException) {
    if (error.statusCode == 401 || error.statusCode == 403) {
      return 'You don\'t have access to this. Try signing in again.';
    }
    return error.message.isEmpty
        ? 'The server returned an error (${error.statusCode}).'
        : error.message;
  }
  if (error is http.ClientException) {
    return 'Couldn\'t reach the control plane. Check your connection and '
        'try again.';
  }
  return '$error';
}

/// A centered icon + title + short explanation + optional action, used for
/// "nothing here yet", "nothing matches", and "failed to load" states so
/// every list screen tells the user why it's empty and what to do next.
///
/// Scrollable (always) so it still works as the child of a
/// [RefreshIndicator].
class StateMessage extends StatelessWidget {
  final IconData icon;
  final String title;
  final String? message;
  final String? actionLabel;
  final IconData? actionIcon;
  final VoidCallback? onAction;
  final bool isError;

  const StateMessage({
    super.key,
    required this.icon,
    required this.title,
    this.message,
    this.actionLabel,
    this.actionIcon,
    this.onAction,
    this.isError = false,
  });

  /// "Couldn't load X" with a Retry button.
  factory StateMessage.error({
    Key? key,
    required String what,
    required Object? error,
    required VoidCallback onRetry,
  }) {
    return StateMessage(
      key: key,
      icon: Icons.cloud_off_outlined,
      title: 'Couldn\'t load $what',
      message: describeLoadError(error),
      actionLabel: 'Retry',
      actionIcon: Icons.refresh,
      onAction: onRetry,
      isError: true,
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final iconColor = isError
        ? theme.colorScheme.error
        : theme.colorScheme.outline;

    return LayoutBuilder(
      builder: (context, constraints) => SingleChildScrollView(
        physics: const AlwaysScrollableScrollPhysics(),
        child: ConstrainedBox(
          constraints: BoxConstraints(minHeight: constraints.maxHeight),
          child: Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 420),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(icon, size: 48, color: iconColor),
                    const SizedBox(height: 12),
                    Text(
                      title,
                      style: theme.textTheme.titleMedium,
                      textAlign: TextAlign.center,
                    ),
                    if (message != null) ...[
                      const SizedBox(height: 6),
                      Text(
                        message!,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                        textAlign: TextAlign.center,
                      ),
                    ],
                    if (actionLabel != null && onAction != null) ...[
                      const SizedBox(height: 16),
                      FilledButton.tonalIcon(
                        onPressed: onAction,
                        icon: Icon(actionIcon ?? Icons.arrow_forward, size: 18),
                        label: Text(actionLabel!),
                      ),
                    ],
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
