import QtQuick
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// The one screen that is a console: it answers "how is this month going" and
// "what needs me" with big figures, not with a grid of equal cards.
Item {
    id: root

    property var router: null
    // Everything below the "stats" key of /v1/creator/summary. Nothing here is
    // derived from a public list: a number either comes from the server or is
    // shown as unknown.
    property var stats: ({})
    property var revenue: ({})
    property var allVideos: []
    property bool loading: false
    property string errorText: ""

    readonly property string myUid: Http.firstOf(Api.me.uid, Api.me.id, "")
    readonly property bool statsReady: stats && stats.scope !== undefined
    readonly property var works: allVideos

    // null means "the server did not give us this number" — never a guess.
    function metric(key) {
        if (!stats || stats[key] === undefined || stats[key] === null)
            return null
        var n = Number(stats[key])
        return isFinite(n) ? n : null
    }

    function metricText(key) {
        var v = metric(key)
        return v === null ? "—" : Http.count(v)
    }

    function basisText() {
        var basis = String((root.stats && root.stats.play_basis) || "")
        if (basis === "legacy_detail_views")
            return qsTr("详情页浏览")
        return basis === "" ? "—" : basis
    }

    function revenueText() {
        if (!revenue || revenue.estimated_revenue === undefined || revenue.estimated_revenue === null)
            return "—"
        var v = Number(revenue.estimated_revenue)
        return isFinite(v) ? v.toFixed(2) : "—"
    }

    function load() {
        loading = true
        errorText = ""
        Api.request("creator.summary", "GET", "/v1/creator/summary")
        Api.requestQuery("creator.works", "/v1/my/videos", { page: 1, page_size: 30 })
    }

    Component.onCompleted: load()

    Connections {
        target: root.router
        function onNavigated(name) {
            if (name === "creator")
                root.load()
        }
        function onLibraryChanged() {
            root.load()
        }
    }

    Connections {
        target: Api

        function onResult(tag, data) {
            var payload = data && data.data ? data.data : data

            if (tag === "creator.summary") {
                root.loading = false
                var body = payload || ({})
                root.stats = body.stats || ({})
                root.revenue = body.revenue || ({})
            } else if (tag === "creator.works") {
                root.loading = false
                root.allVideos = Http.items(data)
            }
        }

        function onFailed(tag, status, code, message) {
            if (tag === "creator.summary" || tag === "creator.works") {
                root.loading = false
                if (status === 401)
                    root.errorText = qsTr("登录之后才能看到创作者中心。")
                else if (root.errorText === "")
                    root.errorText = message
            }
        }
    }

    Flickable {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        contentWidth: width
        contentHeight: column.height
        clip: true

        Column {
            id: column
            width: parent.width
            spacing: 0

            Row {
                width: parent.width

                Column {
                    spacing: 6
                    width: parent.width - reloadButton.width - 20

                    PairTitle { en: "CREATOR"; cn: "创作者" }
                    FieldRow {
                        fields: [
                            Api.authenticated ? Api.userName : qsTr("未登录"),
                            root.metricText("video_count") + qsTr(" 条作品"),
                            Api.state === Api.Ready ? qsTr("已连接") : qsTr("未连接")
                        ]
                    }
                }

                ActionButton {
                    id: reloadButton
                    anchors.verticalCenter: parent.verticalCenter
                    label: qsTr("刷新")
                    icon: "refresh"
                    variant: "ghost"
                    cornerColor: Theme.paper
                    onClicked: root.load()
                }
            }

            Item { height: 14; width: 1 }
            TriStrip { width: 200 }
            Item { height: 18; width: 1 }

            Notice {
                width: parent.width
                visible: root.errorText !== ""
                text: root.errorText
                actionLabel: qsTr("重试")
                onActionClicked: root.load()
            }

            Item { height: root.errorText !== "" ? 14 : 0; width: 1 }

            Notice {
                width: parent.width
                visible: !Api.authenticated
                text: qsTr("这些数字属于账号本人。登录之后才能看到自己的作品和播放。")
                actionLabel: qsTr("去登录")
                onActionClicked: if (root.router) root.router.go("login")
            }

            Item { height: !Api.authenticated ? 14 : 0; width: 1 }

            // Figures: one block carries the accent because it is the number
            // you are supposed to do something about.
            Row {
                width: parent.width
                spacing: 16
                visible: Api.authenticated

                BoardPanel {
                    width: (parent.width - 48) / 3
                    height: 132
                    en: "PLAYS"
                    cn: qsTr("总播放")
                    cutCorners: Qt.BottomRightCorner

                    Text {
                        anchors.left: parent.left
                        anchors.bottom: parent.bottom
                        text: root.metricText("total_plays")
                        color: Theme.ink
                        font.family: Theme.fontField
                        font.pixelSize: 40
                        font.weight: Font.Bold
                        font.features: ({ "tnum": 1 })
                        renderType: Text.NativeRendering
                    }
                }

                BoardPanel {
                    width: (parent.width - 48) / 3
                    height: 132
                    en: "LIKES"
                    cn: qsTr("总点赞")
                    cutCorners: Qt.BottomLeftCorner

                    Text {
                        anchors.left: parent.left
                        anchors.bottom: parent.bottom
                        text: root.metricText("total_likes")
                        color: Theme.ink
                        font.family: Theme.fontField
                        font.pixelSize: 40
                        font.weight: Font.Bold
                        font.features: ({ "tnum": 1 })
                        renderType: Text.NativeRendering
                    }
                }

                BoardPanel {
                    width: (parent.width - 48) / 3
                    height: 132
                    en: "COMMENTS"
                    cn: qsTr("总评论")
                    cutCorners: Qt.TopLeftCorner

                    Text {
                        anchors.left: parent.left
                        anchors.bottom: parent.bottom
                        text: root.metricText("total_comments")
                        color: Theme.ink
                        font.family: Theme.fontField
                        font.pixelSize: 40
                        font.weight: Font.Bold
                        font.features: ({ "tnum": 1 })
                        renderType: Text.NativeRendering
                    }
                }
            }

            Item { height: 10; width: 1 }

            // Say it when a number is missing instead of showing a zero.
            FieldRow {
                visible: root.metric("total_plays") === null
                    || root.metric("total_likes") === null
                    || root.metric("total_comments") === null
                fields: [qsTr("部分数字还在统计，页面会自动更新")]
                size: 11
            }

            Item { height: 6; width: 1 }

            FieldRow {
                fields: [
                    qsTr("关注者 ") + root.metricText("follower_count"),
                    qsTr("作品 ") + root.metricText("video_count"),
                    qsTr("预估收益 ¥") + root.revenueText(),
                    qsTr("统计口径：") + root.basisText()
                ]
                size: 11
            }

            Item { height: 16; width: 1 }

            // Works
            BoardPanel {
                width: parent.width
                height: 60 + Math.min(320, root.works.length * 48)
                en: "WORKS"
                cn: qsTr("我的作品")
                cutCorners: Qt.BottomRightCorner

                Column {
                    anchors.fill: parent
                    spacing: 0

                    Repeater {
                        model: Math.min(root.works.length, 8)

                        delegate: Item {
                            id: workRow
                            required property int index

                            width: parent.width
                            height: 48

                            property var work: root.works[index]

                            Row {
                                anchors.left: parent.left
                                anchors.right: parent.right
                                anchors.verticalCenter: parent.verticalCenter
                                spacing: 12

                                NumberTag {
                                    text: Http.tag(index + 1)
                                    current: index === 0
                                    anchors.verticalCenter: parent.verticalCenter
                                }

                                Text {
                                    width: Math.max(80, parent.width - 300)
                                    anchors.verticalCenter: parent.verticalCenter
                                    text: Http.firstOf(workRow.work.title, qsTr("未命名"))
                                    color: Theme.ink
                                    font.family: Theme.fontText
                                    font.pixelSize: 13
                                    elide: Text.ElideRight
                                    renderType: Text.NativeRendering
                                }

                                FieldRow {
                                    anchors.verticalCenter: parent.verticalCenter
                                    fields: [
                                        Http.count(workRow.work.play_count || 0) + qsTr(" 播放"),
                                        Http.count(workRow.work.like_count || 0) + qsTr(" 赞")
                                    ]
                                    size: 11
                                }
                            }

                            Rectangle {
                                anchors.bottom: parent.bottom
                                width: parent.width
                                height: 1
                                color: Theme.rule
                            }
                        }
                    }

                    Text {
                        visible: root.works.length === 0
                        text: qsTr("还没有作品。发布第一条之后，这里会显示它的播放和互动。")
                        color: Theme.ink2
                        font.family: Theme.fontText
                        font.pixelSize: 13
                        renderType: Text.NativeRendering
                    }
                }
            }

            Item { height: 16; width: 1 }

            Row {
                width: parent.width
                spacing: 16

                ActionButton {
                    label: qsTr("发布视频")
                    icon: "upload"
                    variant: "primary"
                    cornerColor: Theme.paper
                    onClicked: if (root.router) root.router.go("publish")
                }

                ActionButton {
                    label: qsTr("去我的收藏")
                    icon: "bookmark"
                    variant: "ghost"
                    cornerColor: Theme.paper
                    onClicked: if (root.router) root.router.go("collections")
                }

                ActionButton {
                    label: qsTr("退出登录")
                    variant: "danger"
                    cornerColor: Theme.paper
                    onClicked: {
                        Api.logout()
                        if (root.router) root.router.go("login")
                    }
                }
            }

            Item { height: 40; width: 1 }
        }
    }
}
