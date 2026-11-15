ObjC.import('Cocoa');
ObjC.import('WebKit');

function run(argv) {
    var url = (argv && argv.length > 0 && argv[0]) ? String(argv[0]) : "";
    var resDir = (argv && argv.length > 1 && argv[1]) ? String(argv[1]) : "";
    if (!url) return 1;

    var app = $.NSApplication.sharedApplication;
    app.setActivationPolicy(0); // NSApplicationActivationPolicyRegular

    if (resDir) {
        try {
            var icon = $.NSImage.alloc.initWithContentsOfFile(resDir + "/AppIcon.icns");
            if (icon && !icon.isNil()) app.setApplicationIconImage(icon);
        } catch (e) {}
    }

    // Standard minimal macOS menu bar (App + Edit for Cmd+C/V/X/A + Window)
    try {
        var mainMenu = $.NSMenu.alloc.init;

        var appMenuItem = $.NSMenuItem.alloc.init;
        mainMenu.addItem(appMenuItem);
        var appMenu = $.NSMenu.alloc.initWithTitle("ShareNet");
        appMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Скрыть ShareNet", "hide:", "h"));
        appMenu.addItem($.NSMenuItem.separatorItem);
        appMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Выход из ShareNet", "terminate:", "q"));
        appMenuItem.setSubmenu(appMenu);

        var editMenuItem = $.NSMenuItem.alloc.init;
        mainMenu.addItem(editMenuItem);
        var editMenu = $.NSMenu.alloc.initWithTitle("Правка");
        editMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Вырезать", "cut:", "x"));
        editMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Копировать", "copy:", "c"));
        editMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Вставить", "paste:", "v"));
        editMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Выделить всё", "selectAll:", "a"));
        editMenuItem.setSubmenu(editMenu);

        var winMenuItem = $.NSMenuItem.alloc.init;
        mainMenu.addItem(winMenuItem);
        var winMenu = $.NSMenu.alloc.initWithTitle("Окно");
        winMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Свернуть", "performMiniaturize:", "m"));
        winMenu.addItem($.NSMenuItem.alloc.initWithTitleActionKeyEquivalent("Закрыть", "performClose:", "w"));
        winMenuItem.setSubmenu(winMenu);

        app.setMainMenu(mainMenu);
    } catch (e) {}

    var winW = 380, winH = 476;
    var styleMask = 1 | 2 | 4 | 8; // Titled | Closable | Miniaturizable | Resizable
    var rect = $.NSMakeRect(0, 0, winW, winH);
    var win = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(rect, styleMask, 2, false);
    win.setTitle("ShareNet");
    win.setMinSize($.NSMakeSize(350, 440));
    win.setMovable(true);
    win.setMovableByWindowBackground(true);
    win.setOpaque(true);
    win.setBackgroundColor($.NSColor.windowBackgroundColor);
    win.setCollectionBehavior(2); // NSWindowCollectionBehaviorMoveToActiveSpace
    win.center;

    var cfg = $.WKWebViewConfiguration.alloc.init;
    var webView = $.WKWebView.alloc.initWithFrameConfiguration($.NSMakeRect(0, 0, winW, winH), cfg);
    webView.setAutoresizingMask(18);
    try {
        webView.setValueForKey($.NSNumber.numberWithBool(false), "drawsBackground");
    } catch (e) {}
    win.setContentView(webView);

    var req = $.NSURLRequest.requestWithURL($.NSURL.URLWithString(url));
    webView.loadRequest(req);

    win.setReleasedWhenClosed(false);

    // Handle Dock clicks (reopen AppleEvent), app activation, and red close button inside Cocoa runloop
    var nc = $.NSNotificationCenter.defaultCenter;
    nc.addObserverSelectorNameObject(app, "terminate:", "NSWindowWillCloseNotification", win);
    nc.addObserverSelectorNameObject(win, "deminiaturize:", "NSApplicationDidBecomeActiveNotification", app);
    nc.addObserverSelectorNameObject(win, "makeKeyAndOrderFront:", "NSApplicationDidBecomeActiveNotification", app);
    nc.addObserverSelectorNameObject(win, "orderFrontRegardless", "NSApplicationDidBecomeActiveNotification", app);

    var aem = $.NSAppleEventManager.sharedAppleEventManager;
    aem.setEventHandlerAndSelectorForEventClassAndEventID(win, "makeKeyAndOrderFront:", 0x61657674, 0x72617070); // aevt / rapp
    aem.setEventHandlerAndSelectorForEventClassAndEventID(win, "makeKeyAndOrderFront:", 0x61657674, 0x6f617070); // aevt / oapp

    win.makeKeyAndOrderFront(null);
    win.orderFrontRegardless;
    app.activateIgnoringOtherApps(true);
    app.run;
    return 0;
}
