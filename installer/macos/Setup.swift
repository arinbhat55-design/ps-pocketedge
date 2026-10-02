import Cocoa
import SwiftUI

struct Settings: Codable {
    var mode: String
    var runtime: String
    var socket: String
    var database: String
    var databaseURL: String
    var apiURL: String
    var appPath: String
    var dataPath: String
    var cpus: Int
    var memoryGB: Int
    var diskGB: Int
    var autoStart: Bool
    var allowHomebrew: Bool
    var adminEmail: String
    var adminPassword: String

    static var defaults: Settings {
        Settings(mode: "local", runtime: "install-docker", socket: "",
                 database: "install", databaseURL: "", apiURL: "http://localhost:8080",
                 appPath: "/Applications/PS-pocketEdge.app",
                 dataPath: NSHomeDirectory() + "/Library/Application Support/PSpocketEdge/LocalSetup",
                 cpus: min(2, ProcessInfo.processInfo.activeProcessorCount),
                 memoryGB: min(4, max(2, Int(ProcessInfo.processInfo.physicalMemory / (2 * 1024 * 1024 * 1024)))),
                 diskGB: 60, autoStart: true, allowHomebrew: true, adminEmail: "", adminPassword: "")
    }
}

struct SetupEvent: Decodable, Identifiable {
    var component: String
    var status: String
    var message: String?
    var path: String?
    var version: String?
    var done: Bool?
    var ok: Bool?
    var id: String { component }
}

@MainActor final class SetupModel: ObservableObject {
    @Published var plan = Settings.defaults
    @Published var custom = false
    @Published var page = 0
    @Published var busy = false
    @Published var finished = false
    @Published var succeeded = false
    @Published var events: [SetupEvent] = []
    @Published var error = ""
    @Published var detecting = true
    private var process: Process?

    var payload: URL { Bundle.main.resourceURL!.appendingPathComponent("payload") }
    var helper: URL { Bundle.main.executableURL!.deletingLastPathComponent().appendingPathComponent("pe-setup-helper") }
    var reviewPage: Int { 4 }

    func detectExisting() {
        let task = Process(), output = Pipe()
        task.executableURL = helper; task.arguments = ["defaults"]
        task.standardOutput = output
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                try task.run()
                let data = output.fileHandleForReading.readDataToEndOfFile()
                task.waitUntilExit()
                if task.terminationStatus == 0, var saved = try? JSONDecoder().decode(Settings.self, from: data) {
                    saved.cpus = min(saved.cpus, ProcessInfo.processInfo.activeProcessorCount)
                    saved.memoryGB = min(saved.memoryGB, Settings.defaults.memoryGB)
                    let suggested = saved
                    DispatchQueue.main.async { if self.page == 0 { self.plan = suggested }; self.detecting = false }
                } else {
                    DispatchQueue.main.async { self.detecting = false }
                }
            } catch { DispatchQueue.main.async { self.detecting = false } }
        }
    }

    var homebrewRefused: Bool {
        plan.mode == "local" && !plan.allowHomebrew &&
        (plan.runtime.hasPrefix("install-") || plan.database == "install") &&
        !FileManager.default.isExecutableFile(atPath: "/opt/homebrew/bin/brew") &&
        !FileManager.default.isExecutableFile(atPath: "/usr/local/bin/brew")
    }

    func refuse(_ name: String) {
        let alert = NSAlert()
        alert.messageText = "\(name) is required"
        alert.informativeText = "This component is required to complete local setup. Declining it will exit the installer. No installation has started."
        alert.alertStyle = .warning
        alert.addButton(withTitle: "Go back")
        alert.addButton(withTitle: "Exit setup")
        if alert.runModal() == .alertSecondButtonReturn { NSApp.terminate(nil) }
    }

    func next() {
        if page == 0 {
            if !custom { page = reviewPage }
            else { page = plan.mode == "local" ? 1 : 3 }
        } else if page == 1 {
            if plan.runtime == "decline" { refuse("Container runtime"); return }
            page = 2
        } else if page == 2 {
            if plan.database == "decline" { refuse("PostgreSQL"); return }
            page = 3
        } else {
            if homebrewRefused { refuse("Homebrew"); return }
            page = reviewPage
        }
    }

    func back() {
        if page == reviewPage { page = custom ? 3 : 0 }
        else if page == 3 { page = plan.mode == "local" ? 2 : 0 }
        else { page = max(0, page - 1) }
        error = ""
    }

    func record(_ event: SetupEvent) {
        if !event.component.isEmpty {
            if let index = events.firstIndex(where: { $0.component == event.component }) { events[index] = event }
            else { events.append(event) }
        }
        if event.status == "Failed" || (event.done == true && event.ok != true) {
            if let message = event.message, !message.isEmpty { error = message }
        }
    }

    func start() {
        guard !busy else { return }
        if plan.mode == "local" && plan.runtime == "decline" { refuse("Container runtime"); return }
        if plan.mode == "local" && plan.database == "decline" { refuse("PostgreSQL"); return }
        if homebrewRefused { refuse("Homebrew"); return }
        busy = true; finished = false; succeeded = false; error = ""; events = []
        execute("preflight") { [weak self] ok in
            guard let self = self else { return }
            if ok { self.execute("install") { ok in self.busy = false; self.finished = true; self.succeeded = ok } }
            else { self.busy = false; self.finished = true }
        }
    }

    private func execute(_ operation: String, completion: @escaping (Bool) -> Void) {
        let task = Process()
        task.executableURL = helper
        task.arguments = [operation, payload.path]
        let input = Pipe(), output = Pipe(), errors = Pipe()
        task.standardInput = input; task.standardOutput = output; task.standardError = errors
        do {
            let data = try JSONEncoder().encode(plan)
            try task.run()
            process = task
            input.fileHandleForWriting.write(data)
            try input.fileHandleForWriting.close()
        } catch {
            self.error = "Could not start setup: \(error.localizedDescription)"
            completion(false); return
        }
        DispatchQueue.global(qos: .userInitiated).async {
            var pending = Data()
            while true {
                let chunk = output.fileHandleForReading.availableData
                if chunk.isEmpty { break }
                pending.append(chunk)
                while let newline = pending.firstIndex(of: 10) {
                    let line = pending.prefix(upTo: newline)
                    pending.removeSubrange(...newline)
                    if let event = try? JSONDecoder().decode(SetupEvent.self, from: line) {
                        DispatchQueue.main.async { self.record(event) }
                    }
                }
            }
            task.waitUntilExit()
            DispatchQueue.main.async {
                self.process = nil
                if task.terminationStatus != 0 && self.error.isEmpty { self.error = "Setup stopped. Review the checklist and logs, then retry." }
                completion(task.terminationStatus == 0)
            }
        }
    }

    func openApp() {
        guard succeeded, !busy else { return }
        busy = true
        execute("open") { ok in self.busy = false; if ok { NSApp.terminate(nil) } }
    }

    func exit() {
        if busy {
            let alert = NSAlert()
            alert.messageText = "Stop setup?"
            alert.informativeText = "Further installation will stop. Components already installed and existing data will remain. If Homebrew is installing in Terminal, close that operation separately."
            alert.addButton(withTitle: "Continue setup"); alert.addButton(withTitle: "Stop setup")
            if alert.runModal() != .alertSecondButtonReturn { return }
            process?.terminate()
            error = "Setup was stopped. Review the checklist before retrying."
        } else { NSApp.terminate(nil) }
    }

    func chooseFolder(app: Bool) {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true; panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.prompt = "Choose"
        if panel.runModal() == .OK, let url = panel.url {
            if app { plan.appPath = url.appendingPathComponent("PS-pocketEdge.app").path }
            else { plan.dataPath = url.path }
        }
    }
}

struct SetupView: View {
    @ObservedObject var model: SetupModel

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack {
                VStack(alignment: .leading, spacing: 5) {
                    Text("PS-pocketEdge Setup").font(.title.bold())
                    Text("Docker Engine or Podman · PostgreSQL · Desktop app").foregroundColor(.secondary)
                }
                Spacer()
                if model.busy || model.detecting { ProgressView().controlSize(.small) }
            }
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if model.busy || model.finished { checklist }
                    else {
                        switch model.page {
                        case 0: welcome
                        case 1: runtime
                        case 2: database
                        case 3: locations
                        default: review
                        }
                    }
                    if !model.error.isEmpty { Text(model.error).foregroundColor(.red).textSelection(.enabled) }
                }.frame(maxWidth: .infinity, alignment: .leading).padding(.trailing, 6)
            }
            Divider()
            HStack {
                Button(model.busy ? "Stop setup" : "Exit") { model.exit() }
                Spacer()
                if !model.busy && !model.finished && model.page > 0 { Button("Back") { model.back() } }
                if model.finished && !model.busy {
                    if model.succeeded { Button("Finish and open app") { model.openApp() }.keyboardShortcut(.defaultAction) }
                    else {
                        Button("Review choices") { model.finished = false; model.page = 4 }
                        Button("Retry") { model.start() }.keyboardShortcut(.defaultAction)
                    }
                } else if !model.busy {
                    Button(model.page == 4 ? "Install" : "Continue") {
                        if model.page == 4 { model.start() } else { model.next() }
                    }.keyboardShortcut(.defaultAction).disabled(model.detecting)
                }
            }
        }.padding(26).frame(width: 710, height: 650)
    }

    var welcome: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Choose your setup").font(.headline)
            Picker("Installation", selection: $model.custom) {
                Text("Quick install (Recommended)").tag(false)
                Text("Custom install").tag(true)
            }.pickerStyle(.radioGroup)
            Text("Quick install selects default locations, required components, generated credentials, and resource limits. You can review everything before installing.").foregroundColor(.secondary)
            Picker("Where will services run?", selection: $model.plan.mode) {
                Text("Set up this computer").tag("local")
                Text("Connect to an existing server").tag("remote")
            }.pickerStyle(.radioGroup)
            if model.plan.mode == "remote" {
                TextField("Control-plane URL", text: $model.plan.apiURL)
                Text("Only the desktop app is installed. The existing server provides the database, agent, and container runtime.").foregroundColor(.secondary)
            }
            Text("Existing data and containers are preserved. Setup never installs Docker Desktop.").foregroundColor(.secondary)
        }.disabled(model.detecting).onChange(of: model.plan.mode) { _, mode in
            if mode == "local" { model.plan.apiURL = "http://localhost:8080" }
        }
    }

    var runtime: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Container runtime — required").font(.headline)
            Picker("Runtime", selection: $model.plan.runtime) {
                Text("Install Docker Engine using Colima (Recommended)").tag("install-docker")
                Text("Install Podman").tag("install-podman")
                Text("Use existing Docker Engine").tag("existing-docker")
                Text("Use existing Podman").tag("existing-podman")
                Text("Do not install or use a runtime").tag("decline")
            }.pickerStyle(.radioGroup)
            if model.plan.runtime.hasPrefix("existing-") {
                TextField("Container socket, e.g. unix:///Users/you/.colima/default/docker.sock", text: $model.plan.socket)
                Text("The selected runtime must be running; setup verifies its API.").foregroundColor(.secondary)
            } else {
                Text("Docker Engine runs in a dedicated Linux VM on macOS. Existing VM profiles and containers are left alone.").foregroundColor(.secondary)
                Stepper("CPUs: \(model.plan.cpus)", value: $model.plan.cpus, in: 1...max(1, ProcessInfo.processInfo.activeProcessorCount))
                Stepper("RAM: \(model.plan.memoryGB) GB", value: $model.plan.memoryGB, in: 2...max(2, Int(ProcessInfo.processInfo.physicalMemory / (2 * 1024 * 1024 * 1024))))
                Stepper("Disk limit: \(model.plan.diskGB) GB", value: $model.plan.diskGB, in: 10...4096, step: 10)
            }
        }
    }

    var database: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("PostgreSQL — required").font(.headline)
            Picker("Database", selection: $model.plan.database) {
                Text("Install a dedicated PostgreSQL 16 database (Recommended)").tag("install")
                Text("Use an existing PostgreSQL database").tag("existing")
                Text("Do not install or use PostgreSQL").tag("decline")
            }.pickerStyle(.radioGroup)
            if model.plan.database == "existing" {
                SecureField("postgres://user:password@host:port/database?sslmode=require", text: $model.plan.databaseURL)
                TextField("Existing app administrator email (if local automatic access is disabled)", text: $model.plan.adminEmail)
                SecureField("Existing app administrator password", text: $model.plan.adminPassword)
                Text("Use a dedicated PS-pocketEdge database with permission to apply its schema. Setup checks migration history and refuses unknown existing application schemas.").foregroundColor(.secondary)
            } else {
                Text("The dedicated database listens only on 127.0.0.1:55433. Its data is stored inside your selected app data folder. Existing PostgreSQL installations and databases are preserved.").foregroundColor(.secondary)
            }
        }
    }

    var locations: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Locations and startup").font(.headline)
            Text("Application")
            HStack { TextField("App path", text: $model.plan.appPath); Button("Choose…") { model.chooseFolder(app: true) } }
            Text("Configuration, database, logs, backups, and agent state")
            HStack { TextField("Data path", text: $model.plan.dataPath); Button("Choose…") { model.chooseFolder(app: false) } }
            Text("Choose an empty folder inside your home directory, or the same folder used by a previous setup.").foregroundColor(.secondary)
            if model.plan.mode == "local" {
                Toggle("Start local services automatically at login", isOn: $model.plan.autoStart)
                Toggle("Install Homebrew if required", isOn: $model.plan.allowHomebrew)
                Text("If Homebrew is missing, its official installer opens in Terminal and may request your administrator password. Declining a required installation stops setup.").foregroundColor(.secondary)
            }
        }
    }

    var review: some View {
        VStack(alignment: .leading, spacing: 15) {
            Text("Review before installing").font(.headline)
            detail("Mode", model.plan.mode == "local" ? "Set up this computer" : "Existing server")
            detail("Application", model.plan.appPath)
            detail("App data", model.plan.dataPath)
            detail("Control-plane API", model.plan.apiURL)
            if model.plan.mode == "local" {
                detail("Runtime", runtimeLabel)
                if model.plan.runtime.hasPrefix("install-") {
                    detail("VM limits", "\(model.plan.cpus) CPUs · \(model.plan.memoryGB) GB RAM · \(model.plan.diskGB) GB disk")
                    detail("VM storage", model.plan.runtime == "install-docker" ? NSHomeDirectory() + "/.colima/pspocketedge" : "Dedicated Podman machine: pspocketedge")
                } else { detail("Runtime socket", model.plan.socket) }
                detail("PostgreSQL", model.plan.database == "install" ? "PostgreSQL 16 · 127.0.0.1:55433 · \(model.plan.dataPath)/postgres" : "Existing dedicated database (credentials hidden)")
                detail("Background services", "Control plane + enrolled agent")
                detail("Start at login", model.plan.autoStart ? "Yes" : "No")
                detail("Homebrew installation", model.plan.allowHomebrew ? "Allowed if needed" : "Use existing only")
                Text("Network access is needed to download missing packages and the VM image; download sizes vary. Setup checks at least 8 GB of free disk space and available ports. Close PS-pocketEdge before installation.").foregroundColor(.secondary)
            }
            Text("Installation in /Applications may request administrator permission. Database credentials and app secrets are generated and stored in a protected credentials.json file. Required components must pass verification before setup can finish.").foregroundColor(.secondary)
        }
    }

    var runtimeLabel: String {
        switch model.plan.runtime {
        case "install-docker": return "Install Docker Engine + Colima + Docker CLI, Compose and Buildx"
        case "install-podman": return "Install Podman"
        case "existing-docker": return "Use existing Docker Engine"
        default: return "Use existing Podman"
        }
    }

    func detail(_ title: String, _ value: String) -> some View {
        HStack(alignment: .top) {
            Text(title).foregroundColor(.secondary).frame(width: 155, alignment: .leading)
            Text(value).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    var checklist: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(model.busy ? "Installing and verifying…" : model.succeeded ? "Setup complete" : "Setup stopped").font(.headline)
            ForEach(model.events) { event in
                HStack(alignment: .top, spacing: 12) {
                    Image(systemName: event.status == "Failed" ? "xmark.circle.fill" : event.status == "Installing" ? "clock" : event.status == "Skipped" ? "minus.circle" : "checkmark.circle.fill")
                        .foregroundColor(event.status == "Failed" ? .red : event.status == "Installing" || event.status == "Skipped" ? .secondary : .green)
                    VStack(alignment: .leading, spacing: 3) {
                        Text("\(event.component) — \(event.status)").fontWeight(.medium)
                        if let version = event.version, !version.isEmpty { Text(version).font(.caption).foregroundColor(.secondary) }
                        if let path = event.path, !path.isEmpty { Text(path).font(.caption).textSelection(.enabled) }
                        if let message = event.message, !message.isEmpty { Text(message).font(.caption).foregroundColor(.secondary) }
                    }
                }
            }
            Button("Open logs and checklist folder") { NSWorkspace.shared.open(URL(fileURLWithPath: model.plan.dataPath)) }
            if !model.succeeded && !model.busy {
                Text("Previously installed components and all existing data remain. Retry rechecks each component; it does not recreate the database.").foregroundColor(.secondary)
            }
        }
    }
}

final class SetupDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    weak var model: SetupModel?
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if let model = model, model.busy { model.exit(); return .terminateCancel }
        return .terminateNow
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if let model = model, model.busy { model.exit(); return false }
        return true
    }
}

@main struct PocketEdgeSetup: App {
    @NSApplicationDelegateAdaptor(SetupDelegate.self) var delegate
    @StateObject private var model = SetupModel()
    var body: some Scene {
        WindowGroup("PS-pocketEdge Setup") {
            SetupView(model: model)
                .onAppear {
                    delegate.model = model; model.detectExisting(); NSApp.activate(ignoringOtherApps: true)
                    DispatchQueue.main.async {
                        for window in NSApp.windows where window.title == "PS-pocketEdge Setup" { window.delegate = delegate }
                    }
                }
        }.windowResizability(.contentSize)
    }
}
