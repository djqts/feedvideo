import QtQuick
import QtQuick.Controls
import QtQuick.Window
import QtQuick.Layouts
import FeedVideo.Core 1.0
import "shell"
import "screens"
import "components"

ApplicationWindow {
    id: window

    visible: true
    width: 1280
    height: 800
    minimumWidth: 1024
    minimumHeight: 680
    title: "FeedVideo"
    color: Theme.paper
    flags: Qt.Window | Qt.FramelessWindowHint

    Router { id: router }

    Component.onCompleted: {
        Theme.enterScreen(router.current)
        Api.probe()
        if (demoMode === "publish" || demoMode === "publish-dialog")
            router.go("publish")
        else if (demoMode === "video" || demoMode === "report")
            router.go("video", { id: "6afc8fdd9cd74c86163f293e" })
        else if (demoMode === "creator")
            router.go("creator")
        else if (demoMode === "gone")
            router.go("video", { id: "000000000000000000000000" })
        else if (demoMode === "deleted") {
            router.go("video", { id: "6afc8fdd9cd74c86163f293e" })
            demoDeleteTimer.start()
        }
    }

    Timer {
        id: demoDeleteTimer
        interval: 2500
        onTriggered: router.videoDeleted("6afc8fdd9cd74c86163f293e")
    }

    Connections {
        target: router
        function onNavigated(name) {
            Theme.enterScreen(name)
        }
    }

    function screenIndex() {
        switch (router.current) {
        case "search": return 1
        case "video": return 2
        case "notifications": return 3
        case "collections": return 4
        case "history": return 5
        case "creator": return 6
        case "publish": return 7
        case "settings": return 8
        case "specimen": return 9
        case "login": return 10
        default: return 0
        }
    }

    // F2 opens the component specimen while developing (see SpecimenScreen).
    Shortcut {
        sequence: "F2"
        onActivated: router.go(router.current === "specimen" ? "feed" : "specimen")
    }

    // Ctrl+1..8 jump between sections; Ctrl+K focuses search.
    Shortcut { sequence: "Ctrl+1"; onActivated: router.go("feed") }
    Shortcut { sequence: "Ctrl+2"; onActivated: router.go("search") }
    Shortcut { sequence: "Ctrl+3"; onActivated: router.go("notifications") }
    Shortcut { sequence: "Ctrl+4"; onActivated: router.go("collections") }
    Shortcut { sequence: "Ctrl+5"; onActivated: router.go("history") }
    Shortcut { sequence: "Ctrl+6"; onActivated: router.go("creator") }
    Shortcut { sequence: "Ctrl+7"; onActivated: router.go("publish") }
    Shortcut { sequence: "Ctrl+8"; onActivated: router.go("settings") }
    Shortcut { sequence: "Ctrl+9"; onActivated: router.go("login") }

    function section() {
        switch (router.current) {
        case "search": return "搜索"
        case "video": return "视频"
        case "notifications": return "通知"
        case "collections": return "收藏"
        case "history": return "历史"
        case "creator": return "创作者"
        case "publish": return "发布"
        case "settings": return "设置"
        default: return "推荐"
        }
    }

    Item {
        anchors.fill: parent

        TitleBar {
            id: titleBar
            anchors.top: parent.top
            anchors.left: parent.left
            anchors.right: parent.right
            section: window.section()
            connection: Api.state
        }

        Row {
            anchors.top: titleBar.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom

            SideNav {
                id: nav
                height: parent.height
                visible: router.current !== "login"
                current: router.current
                authenticated: Api.authenticated
                accountLabel: Api.userName
                onNavigate: function (name) { router.go(name) }
                onThemeClicked: Theme.toggleMode()
                onAccountClicked: router.go(Api.authenticated ? "creator" : "login")
            }

            StackLayout {
                width: parent.width - (nav.visible ? nav.width : 0)
                height: parent.height
                currentIndex: window.screenIndex()

                FeedScreen { router: router }
                SearchScreen { router: router }
                VideoScreen { router: router }
                NotificationsScreen { router: router }
                CollectionsScreen { router: router }
                HistoryScreen { router: router }
                CreatorScreen { router: router }
                PublishScreen { router: router }
                SettingsScreen { router: router }
                SpecimenScreen { router: router }
                LoginScreen { router: router }
            }
        }

        Toast {
            id: toast
            anchors.horizontalCenter: parent.horizontalCenter
            anchors.bottom: parent.bottom
            anchors.bottomMargin: 28
        }
    }
}
