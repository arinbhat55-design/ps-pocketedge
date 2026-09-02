import 'package:app/features/containers/container_config_form.dart';
import 'package:app/models/container.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  Future<ContainerConfig> pumpAndSubmit(
    WidgetTester tester, {
    ContainerConfig? initial,
    required Future<void> Function(WidgetTester) fillResourceFields,
  }) async {
    // The form (image/name/command/env/ports/volumes/restart-policy/
    // resource-limits) is taller than the default 800x600 test surface,
    // and it renders inside a SingleChildScrollView — rather than relying
    // on scroll-into-view timing before tapping the submit button, size
    // the test surface tall enough that everything (including the button)
    // is laid out on-screen at once.
    tester.view.physicalSize = const Size(800, 2000);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    ContainerConfig? submitted;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ContainerConfigForm(
            title: 'Create container',
            submitLabel: 'Create',
            initial: initial,
            loading: false,
            onSubmit: (config) => submitted = config,
          ),
        ),
      ),
    );

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Image'),
      'nginx:latest',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Container name'),
      'web',
    );
    await fillResourceFields(tester);

    await tester.tap(find.widgetWithText(FilledButton, 'Create'));
    await tester.pump();

    expect(submitted, isNotNull);
    return submitted!;
  }

  group('ContainerConfigForm resource limits', () {
    testWidgets('blank resource fields submit as 0 (unlimited)', (
      tester,
    ) async {
      final config = await pumpAndSubmit(
        tester,
        fillResourceFields: (_) async {},
      );

      expect(config.nanoCpus, 0);
      expect(config.memoryLimitBytes, 0);
      expect(config.memoryReservationBytes, 0);
      expect(config.pidsLimit, 0);
    });

    testWidgets('converts CPU cores to nanoCPUs and MB to bytes', (
      tester,
    ) async {
      final config = await pumpAndSubmit(
        tester,
        fillResourceFields: (tester) async {
          await tester.enterText(
            find.widgetWithText(
              TextFormField,
              'CPU limit, in cores (blank = unlimited)',
            ),
            '1.5',
          );
          await tester.enterText(
            find.widgetWithText(
              TextFormField,
              'Memory limit, in MB (blank = unlimited)',
            ),
            '128',
          );
          await tester.enterText(
            find.widgetWithText(
              TextFormField,
              'Memory reservation, in MB (blank = none)',
            ),
            '64',
          );
          await tester.enterText(
            find.widgetWithText(
              TextFormField,
              'Process limit (blank = unlimited)',
            ),
            '100',
          );
        },
      );

      expect(config.nanoCpus, 1500000000);
      expect(config.memoryLimitBytes, 128 * 1024 * 1024);
      expect(config.memoryReservationBytes, 64 * 1024 * 1024);
      expect(config.pidsLimit, 100);
    });

    testWidgets('pre-fills resource fields from initial config', (
      tester,
    ) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ContainerConfigForm(
              title: 'Recreate container',
              submitLabel: 'Recreate',
              initial: const ContainerConfig(
                image: 'nginx:latest',
                name: 'web',
                nanoCpus: 500000000,
                memoryLimitBytes: 67108864,
              ),
              loading: false,
              onSubmit: (_) {},
            ),
          ),
        ),
      );

      expect(find.text('0.50'), findsOneWidget);
      expect(find.text('64'), findsOneWidget);
    });
  });
}
