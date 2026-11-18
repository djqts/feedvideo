import QtQuick
import QtQuick.Controls
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// The landing screen. A running archive of what is playing now, with the
// preview open in place instead of a separate player page.
Item {
    id: root

    property var router: null
    property var rows: []
    property bool loading: false
    property string feedType: "hot"
    property string errorText: ""
    property int selectedIndex: -1
    // The feed takes a size window plus an after cursor; the server ranks at most
    // 50 candidates, so "more" stops when the ranking runs out.
    readonly property int feedPageSize: 20
    property bool hasMore: false
    property string feedCursor: ""
    property int feedToken: 0
    property bool feedResetRequest: true
    // Distinguish "nothing matched" from "you follow nobody": the second one has
    // a way out, the first one does not.
    readonly property bool followingEmpty: !loading && errorText === ""
        && feedType === "following" && rows.length === 0

    function reload() {
        loading = true
        errorText = ""
        feedResetRequest = true
        feedCursor = ""
        feedToken = feedToken + 1
        Api.requestQuery("feed.page:" + feedToken, "/v1/feed", { type: feedType, size: feedPageSize })
    }

    function loadMore() {
        if (loading || !hasMore)
            return
        loading = true
        feedResetRequest = false
        feedToken = feedToken + 1
        var query = { type: feedType, size: feedPageSize }
        if (feedCursor !== "")
            query.after = feedCursor
        Api.requestQuery("feed.page:" + feedToken, "/v1/feed", query)
    }

    function appendUnique(existing, batch) {
        var seen = {}
        for (var i = 0; i < existing.length; i++)
            seen[String(existing[i].id)] = true
        var out = existing.slice()
        for (var j = 0; j < batch.length; j++) {
            if (!seen[String(batch[j].id)])
                out.push(batch[j])
        }
        return out
    }

    Component.onCompleted: {
        if (demoMode === "following")
            feedType = "following"
        reload()
    }

    // Losing the session while on "关注" must not leave a tab that can no
    // longer exist.
    Connections {
        target: Api
        function onSessionChanged() {
            if (!Api.authenticated && root.feedType === "following") {
                root.feedType = "hot"
                root.reload()
            }
        }
    }

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag.indexOf("feed.page:") !== 0)
                return
            var token = Http.num(tag.substring("feed.page:".length))
            if (token !== root.feedToken)
                return // a newer request already replaced this one
            root.loading = false
            var list = Http.items(data)
            var payload = data && data.data ? data.data : data
            var batch = []
            for (var i = 0; i < list.length; i++)
                batch.push(Http.video(list[i]))
            root.hasMore = !!(payload && payload.has_more)
            root.rows = root.feedResetRequest ? batch : root.appendUnique(root.rows, batch)
            if (root.rows.length > 0)
                root.feedCursor = String(root.rows[root.rows.length - 1].id)
        }

        function onFailed(tag, status, code, message) {
            if (tag !== "feed.page")
                return
            root.loading = false
            root.errorText = message
        }
    }

    Column {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        spacing: 0

        // Header
        Row {
            width: parent.width
            spacing: 20

            Column {
                spacing: 6
                width: parent.width - buttons.width - 20

                PairTitle { en: "FEED"; cn: "推荐" }

                FieldRow {
                    fields: [
                        root.rows.length + qsTr(" 条"),
                        root.feedType === "hot" ? qsTr("热门") : qsTr("推荐"),
                        Api.state === Api.Ready ? qsTr("已连接") : qsTr("未连接")
                    ]
                }
            }

            Row {
                id: buttons
                spacing: 10
                anchors.verticalCenter: parent.verticalCenter

                ActionButton {
                    label: qsTr("刷新")
                    icon: "refresh"
                    variant: "ghost"
                    onClicked: root.reload()
                }

                ActionButton {
                    label: qsTr("搜索")
                    icon: "search"
                    variant: "primary"
                    onClicked: if (root.router) root.router.go("search")
                }
            }
        }

        Item { height: 14; width: 1 }

        TriStrip { width: 180 }

        Item { height: 16; width: 1 }

        // Filters
        Row {
            spacing: 8

            FilterChip {
                label: qsTr("热门")
                selected: root.feedType === "hot"
                onToggled: { root.feedType = "hot"; root.reload() }
            }
            FilterChip {
                label: qsTr("推荐")
                selected: root.feedType === "recommend"
                onToggled: { root.feedType = "recommend"; root.reload() }
            }
            FilterChip {
                label: qsTr("关注")
                // Following only exists for a signed-in account; showing an
                // empty tab to a guest would be a dead end.
                visible: Api.authenticated
                selected: root.feedType === "following"
                onToggled: { root.feedType = "following"; root.reload() }
            }
        }

        Item { height: 12; width: 1 }

        Notice {
            visible: root.errorText !== ""
            width: parent.width
            text: root.errorText !== "" ? root.errorText : ""
            actionLabel: qsTr("重试")
            onActionClicked: root.reload()
        }

        Item { height: root.errorText !== "" ? 12 : 0; width: 1 }

        // The archive
        Item {
            width: parent.width
            height: parent.height - y

            ListView {
                id: list
                anchors.fill: parent
                clip: true
                model: root.rows
                spacing: 0
                boundsBehavior: Flickable.StopAtBounds

                delegate: FeedRow {
                    required property int index
                    required property var modelData

                    width: list.width
                    video: modelData
                    number: Http.tag(index + 1)
                    expanded: root.selectedIndex === index
                    onActivated: root.selectedIndex = (root.selectedIndex === index ? -1 : index)
                    onDetailRequested: function (id) {
                        if (root.router && id !== "")
                            root.router.go("video", { id: id })
                    }
                }

                footer: Item {
                    width: list.width
                    visible: root.hasMore && root.rows.length > 0
                    height: visible ? 56 * Theme.density : 0

                    ActionButton {
                        anchors.centerIn: parent
                        label: root.loading ? qsTr("读取中…") : qsTr("加载更多")
                        variant: "ghost"
                        cornerColor: Theme.paper
                        enabled: !root.loading
                        onClicked: root.loadMore()
                    }
                }

                // Empty state: a hatched frame with one full sentence.
                Item {
                    anchors.fill: parent
                    visible: !root.loading && root.rows.length === 0

                    Rectangle {
                        anchors.fill: parent
                        color: "transparent"
                        border.color: Theme.rule
                        border.width: 1
                    }

                    Hatch {
                        anchors.fill: parent
                        opacity: 0.5
                    }

                    Column {
                        anchors.centerIn: parent
                        spacing: 12

                        Text {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: root.errorText !== ""
                                ? qsTr("还没有连上服务端，先确认地址再刷新。")
                                : root.followingEmpty
                                    ? qsTr("你还没关注任何人。关注之后，他们的新视频会出现在这里。")
                                    : qsTr("这里会按顺序列出正在流传的视频，现在还没有一条。")
                            color: Theme.ink
                            font.family: Theme.fontText
                            font.pixelSize: 14
                            renderType: Text.NativeRendering

                            Rectangle {
                                anchors.fill: parent
                                anchors.margins: -8
                                color: Theme.paper
                                z: -1
                            }
                        }

                        Row {
                            anchors.horizontalCenter: parent.horizontalCenter
                            spacing: 10

                            // A dead end needs a way out; the generic empty state
                            // only needs a retry.
                            ActionButton {
                                visible: root.followingEmpty
                                label: qsTr("去推荐看看")
                                variant: "primary"
                                cornerColor: Theme.paper
                                onClicked: {
                                    root.feedType = "hot"
                                    root.reload()
                                }
                            }

                            ActionButton {
                                visible: root.followingEmpty
                                label: qsTr("去发现")
                                icon: "search"
                                variant: "ghost"
                                cornerColor: Theme.paper
                                onClicked: if (root.router) root.router.go("search")
                            }

                            ActionButton {
                                visible: !root.followingEmpty
                                label: qsTr("重新获取")
                                variant: "primary"
                                cornerColor: Theme.paper
                                onClicked: root.reload()
                            }
                        }
                    }
                }
            }
        }
    }
}
