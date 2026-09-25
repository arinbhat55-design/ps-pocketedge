import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/deployment.dart';
import '../../widgets/status_pill.dart';
import '../../theme/app_theme.dart';

/// Tone for a deployment/revision phase or status: settled-good is
/// healthy, anything needing attention or still in flight is warning,
/// failures are failed, and inactive states are neutral.
StatusTone phaseTone(String phase) {
  switch (phase) {
    case 'running':
    case 'healthy':
    case 'executed':
    case 'completed':
      return StatusTone.healthy;
    case 'failed':
    case 'unhealthy':
    case 'rejected':
      return StatusTone.failed;
    case 'stopped':
    case 'scheduled':
    case 'pending':
    case 'removed':
    case 'cancelled':
    case 'unknown':
      return StatusTone.neutral;
    default:
      // rolled_back, awaiting/pending_approval, and in-progress phases.
      return StatusTone.warning;
  }
}

/// Colour for a deployment/revision phase or status.
Color phaseColor(String phase) => phaseTone(phase).color;

/// "awaiting_approval" -> "Awaiting approval".
String humanizePhase(String phase) {
  if (phase.isEmpty) return phase;
  final s = phase.replaceAll('_', ' ');
  return s[0].toUpperCase() + s.substring(1);
}

/// A deployment status as a [StatusPill].
class StatusDot extends StatelessWidget {
  final String status;
  final String? label;

  const StatusDot({super.key, required this.status, this.label});

  @override
  Widget build(BuildContext context) {
    return StatusPill(
      label: label ?? humanizePhase(status),
      tone: phaseTone(status),
    );
  }
}

/// Post-deployment health verification badge.
class HealthBadge extends StatelessWidget {
  final String status;
  final String message;

  const HealthBadge({super.key, required this.status, this.message = ''});

  @override
  Widget build(BuildContext context) {
    final (IconData icon, String label) = switch (status) {
      'healthy' => (Icons.verified_outlined, 'Verified healthy'),
      'unhealthy' => (Icons.report_gmailerrorred_outlined, 'Unhealthy'),
      'verifying' => (Icons.hourglass_top, 'Verifying health'),
      _ => (Icons.help_outline, 'Health not verified'),
    };
    final color = status == 'unknown' ? AppColors.neutral : phaseColor(status);
    return Tooltip(
      message: message.isEmpty ? label : message,
      child: Chip(
        visualDensity: VisualDensity.compact,
        avatar: Icon(icon, size: 16, color: color),
        label: Text(label),
      ),
    );
  }
}

/// Runs a governed deployment action (deploy, redeploy, rollback, scale,
/// promote...). When the environment's maintenance window blocks it, asks
/// whether to schedule it for the next window or (admins only) run it now
/// anyway, and retries accordingly. Reports the outcome — dispatched,
/// waiting for approval, or scheduled — in a snackbar, and returns it (null
/// if cancelled or failed).
Future<DeploymentActionOutcome?> runGatedAction(
  BuildContext context,
  Future<DeploymentActionOutcome> Function(GateOptions gate) action, {
  String failurePrefix = 'Failed',
  bool showOutcome = true,
}) async {
  final messenger = ScaffoldMessenger.of(context);
  var gate = GateOptions.none;
  while (true) {
    try {
      final outcome = await action(gate);
      if (showOutcome) {
        messenger.showSnackBar(SnackBar(content: Text(outcome.describe())));
      }
      return outcome;
    } on MaintenanceWindowException catch (e) {
      if (!context.mounted) return null;
      final choice = await showDialog<GateOptions>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Outside the maintenance window'),
          content: Text(
            '${e.message}.'
            '${e.nextWindow == null ? '' : '\n\nNext window: ${formatTimestamp(e.nextWindow!)}'}',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('Cancel'),
            ),
            if (e.canOverride)
              TextButton(
                onPressed: () => Navigator.of(
                  context,
                ).pop(const GateOptions(overrideMaintenanceWindow: true)),
                child: const Text('Override — run now'),
              ),
            if (e.nextWindow != null)
              FilledButton(
                onPressed: () => Navigator.of(
                  context,
                ).pop(const GateOptions(scheduleForMaintenanceWindow: true)),
                child: const Text('Schedule for next window'),
              ),
          ],
        ),
      );
      if (choice == null) return null;
      gate = choice;
    } catch (e) {
      final text = e is ApiException ? e.message : '$e';
      messenger.showSnackBar(SnackBar(content: Text('$failurePrefix: $text')));
      return null;
    }
  }
}

/// A read-only monospace YAML viewer dialog.
Future<void> showYamlDialog(BuildContext context, String title, String yaml) {
  return showDialog<void>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text(title),
      content: SizedBox(
        width: 620,
        height: 480,
        child: SingleChildScrollView(
          child: SelectableText(
            yaml,
            style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
          ),
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
      ],
    ),
  );
}

/// Prompts for an optional free-text comment (approve/reject/cancel).
/// Returns null if dismissed.
Future<String?> promptComment(
  BuildContext context, {
  required String title,
  required String action,
  String hint = 'Comment (optional)',
  bool destructive = false,
}) {
  return showDialog<String>(
    context: context,
    builder: (_) => _CommentDialog(
      title: title,
      action: action,
      hint: hint,
      destructive: destructive,
    ),
  );
}

/// Owns its controller so it's disposed with the dialog, after the close
/// animation — not while the closing dialog is still building it.
class _CommentDialog extends StatefulWidget {
  final String title;
  final String action;
  final String hint;
  final bool destructive;

  const _CommentDialog({
    required this.title,
    required this.action,
    required this.hint,
    required this.destructive,
  });

  @override
  State<_CommentDialog> createState() => _CommentDialogState();
}

class _CommentDialogState extends State<_CommentDialog> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(widget.title),
      content: SizedBox(
        width: 420,
        child: TextField(
          controller: _controller,
          autofocus: true,
          maxLines: 3,
          decoration: InputDecoration(labelText: widget.hint),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          style: widget.destructive
              ? FilledButton.styleFrom(
                  backgroundColor: Theme.of(context).colorScheme.error,
                )
              : null,
          onPressed: () => Navigator.of(context).pop(_controller.text.trim()),
          child: Text(widget.action),
        ),
      ],
    );
  }
}

/// Height shared by every control in a list's filter bar, so search boxes,
/// dropdowns, and chips line up on one baseline.
const kFilterControlHeight = 40.0;

InputDecoration _filterDecoration({String? hint, Widget? prefixIcon}) {
  return InputDecoration(
    isDense: true,
    hintText: hint,
    prefixIcon: prefixIcon,
    prefixIconConstraints: const BoxConstraints(minWidth: 36, minHeight: 36),
    border: const OutlineInputBorder(),
    contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
  );
}

/// A search box for a filter bar.
class FilterSearchField extends StatelessWidget {
  final TextEditingController controller;
  final String hint;
  final ValueChanged<String> onSubmitted;
  final double width;

  const FilterSearchField({
    super.key,
    required this.controller,
    required this.hint,
    required this.onSubmitted,
    this.width = 280,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: width,
      height: kFilterControlHeight,
      child: TextField(
        controller: controller,
        textAlignVertical: TextAlignVertical.center,
        decoration: _filterDecoration(
          hint: hint,
          prefixIcon: const Icon(Icons.search, size: 18),
        ),
        onSubmitted: onSubmitted,
      ),
    );
  }
}

/// A labelled dropdown for a filter bar; a null value means "all".
class FilterDropdown<T> extends StatelessWidget {
  final T? value;
  final String allLabel;
  final Map<T, String> options;
  final ValueChanged<T?> onChanged;
  final double width;

  const FilterDropdown({
    super.key,
    required this.value,
    required this.allLabel,
    required this.options,
    required this.onChanged,
    this.width = 190,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: width,
      height: kFilterControlHeight,
      child: DropdownButtonFormField<T?>(
        initialValue: value,
        isExpanded: true,
        isDense: true,
        decoration: _filterDecoration(),
        items: [
          DropdownMenuItem<T?>(value: null, child: Text(allLabel)),
          for (final e in options.entries)
            DropdownMenuItem<T?>(value: e.key, child: Text(e.value)),
        ],
        onChanged: onChanged,
      ),
    );
  }
}

/// A compact status chip with the same footprint as [HealthBadge], for rows
/// that have a status but no health result yet.
class StatusChip extends StatelessWidget {
  final String status;

  const StatusChip({super.key, required this.status});

  @override
  Widget build(BuildContext context) {
    return StatusPill(label: humanizePhase(status), tone: phaseTone(status));
  }
}

/// A small "icon  Label: value" line for secondary details.
class InfoLine extends StatelessWidget {
  final IconData icon;
  final String label;
  final String value;

  const InfoLine({
    super.key,
    required this.icon,
    required this.label,
    required this.value,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final muted = theme.colorScheme.onSurfaceVariant;
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 16, color: muted),
          const SizedBox(width: 8),
          Expanded(
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(
                    text: '$label: ',
                    style: TextStyle(color: muted),
                  ),
                  TextSpan(text: value),
                ],
              ),
              style: theme.textTheme.bodySmall,
            ),
          ),
        ],
      ),
    );
  }
}
