import QtQuick
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// What you watched, newest first. The server records the events; this screen
// only lists them.
Item {
    id: root

    property var router: null
    property var rows: []
    property bool loading: false
    property string errorText: ""
    property int selectedIndex: -1
    property string nextCursor: ""
    property bool hasMore: false
    property bool loadingMore: false

    function load(reset) {
        if (reset) {
            rows = []
            nextCursor = ""
            selectedIndex = -1
            loading = true
        } else {
            loadingMore = true
        }
        errorText = ""
        var query = { size: 20 }
        if (!reset && root.nextCursor !== "")
            query.cursor = root.nextCursor
        Api.requestQuery("history.events", "/v1/behavior/events", query)
    }

    function loadMore() {
        if (loadingMore || !hasMore)
            return
        load(false)
    }

    Component.onCompleted: load(true)

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag !== "history.events")
                return
            root.loading = false
            root.loadingMore = false
            var payload = data && data.data ? data.data : data
            var list = Http.items(data)
            var out = []
            for (var i = 0; i < list.length; i++) {
                var e = list[i]
                out.push(e.video || e)
            }
            root.hasMore = !!(payload && payload.has_more)
            root.nextCursor = payload && payload.next_cursor ? String(payload.next_cursor) : ""
            root.rows = root.rows.length > 0 ? root.rows.concat(out) : out
        }

        function onFailed(tag, status, code, message) {
            if (tag !== "history.events")
                return
            root.loading = false
            root.loadingMore = false
            root.errorText = status === 401 ? qsTr("登录之后才能看到观看记录。") : message
        }
    }

    Column {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        spacing: 0

        Row {
            width: parent.width

            Column {
                spacing: 6
                width: parent.width - refreshButton.width - 20

                PairTitle { en: "HISTORY"; cn: "历史" }
                FieldRow { fields: [root.rows.length + qsTr(" 条")] }
            }

            ActionButton {
                id: refreshButton
                anchors.verticalCenter: parent.verticalCenter
                label: qsTr("刷新")
                icon: "refresh"
                variant: "ghost"
                cornerColor: Theme.paper
                onClicked: root.load(true)
            }
        }

        Item { height: 14; width: 1 }
        TriStrip { width: 160 }
        Item { height: 12; width: 1 }

        Notice {
            width: parent.width
            visible: root.errorText !== ""
            text: root.errorText
            actionLabel: Api.authenticated ? qsTr("重试") : qsTr("去登录")
            onActionClicked: {
                if (Api.authenticated)
                    root.load()
                else if (root.router)
                    root.router.go("login")
            }
        }

        Item {
            width: parent.width
            height: parent.height - y - 6

            ListView {
                id: list
                anchors.fill: parent
                clip: true
                model: root.rows
                boundsBehavior: Flickable.StopAtBounds

                delegate: FeedRow {
                    required property var modelData
                    required property int index

                    width: list.width
                    video: Http.video(modelData)
                    number: Http.tag(index + 1)
                    expanded: root.selectedIndex === index
                    onActivated: root.selectedIndex = (root.selectedIndex === index ? -1 : index)
                }

                footer: Item {
                    width: list.width
                    visible: root.hasMore && root.rows.length > 0
                    height: visible ? 56 : 0

                    ActionButton {
                        anchors.centerIn: parent
                        label: root.loadingMore ? qsTr("读取中…") : qsTr("加载更多")
                        variant: "ghost"
                        cornerColor: Theme.paper
                        enabled: !root.loadingMore
                        onClicked: root.loadMore()
                    }
                }
            }

            Text {
                anchors.centerIn: parent
                visible: !root.loading && root.rows.length === 0 && root.errorText === ""
                text: qsTr("还没有观看记录。看过的视频会按时间排在这里。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 14
                renderType: Text.NativeRendering
            }
        }
    }
}
