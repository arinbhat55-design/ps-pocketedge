// Run against a fresh local control plane:
// flutter test integration_test/local_smoke_test.dart -d macos \
//   --dart-define=CONTROL_PLANE_URL=http://localhost:18080
import 'package:app/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('local session opens every main module', (tester) async {
    await tester.binding.setSurfaceSize(const Size(1280, 900));
    await tester.pumpWidget(const PSPocketEdgeApp());

    Future<void> waitFor(Finder finder) async {
      final deadline = DateTime.now().add(const Duration(seconds: 20));
      while (DateTime.now().isBefore(deadline)) {
        await tester.pump();
        if (finder.evaluate().isNotEmpty) return;
        await Future<void>.delayed(const Duration(milliseconds: 200));
      }
      fail('Timed out waiting for $finder');
    }

    await waitFor(find.byType(NavigationRail));
    final modules = [
      'Servers',
      'Containers',
      'Alerts',
      'Images',
      'Networks',
      'Volumes',
      'Deploy',
      'Databases',
      'Kubernetes',
      'Users',
      'Settings',
    ];
    for (final label in modules) {
      // ignore: avoid_print
      print('OPEN MODULE $label');
      final destination = find.descendant(
        of: find.byType(NavigationRail),
        matching: find.text(label, skipOffstage: false),
      );
      await tester.ensureVisible(destination);
      await tester.tap(destination);
      final end = DateTime.now().add(const Duration(milliseconds: 700));
      while (DateTime.now().isBefore(end)) {
        await Future<void>.delayed(const Duration(milliseconds: 100));
        await tester.pump();
      }
      expect(tester.takeException(), isNull, reason: 'Module: $label');
    }

    await tester.tap(find.text('Deploy'));
    await tester.pump();
    await waitFor(find.text('New Compose file'));
    await tester.tap(find.text('New Compose file').first);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull, reason: 'Open Compose editor');
  });
}
