import QtQuick
import QtQuick.Dialogs
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// Publishing. Pick a file, write a title, watch the segments fill, then the
// video exists on the server.
Item {
    id: root

    property var router: null
    property string filePath: ""
    property string title: ""
    property string description: ""
    property string tags: ""
    property real progress: 0
    property bool uploading: false
    property string uploadId: ""
    property string publishKey: ""
    property var submissions: []
    property bool loadingSubmissions: false
    // The publish page only needs a glance: five latest. "查看全部" switches the
    // same block to a paged list instead of loading fifty rows up front.
    property bool showAll: false
    property bool scrollToSubmissions: false
    property int submissionTotal: 0
    property bool hasMoreSubmissions: false
    property string submissionCursor: ""
    property int submissionToken: 0
    property bool submissionResetRequest: true
    property string pendingDelete: ""
    property string retrying: ""

    readonly property string myUid: Http.firstOf(Api.me.uid, Api.me.id, "")

    // Same cursor semantics as comments: "before" is the last id we hold, so a
    // video published in between cannot shift the window.
    function loadSubmissions(reset) {
        if (myUid === "") {
            submissions = []
            return
        }
        if (reset !== false) {
            submissions = []
            submissionCursor = ""
        }
        loadingSubmissions = true
        submissionResetRequest = reset !== false
        submissionToken = submissionToken + 1
        var query = { page: 1, page_size: root.showAll ? 20 : 5 }
        if (reset === false && submissionCursor !== "")
            query.before = submissionCursor
        Api.requestQuery("publish.mine:" + submissionToken, "/v1/my/videos", query)
    }

    function loadMoreSubmissions() {
        if (loadingSubmissions || !hasMoreSubmissions)
            return
        loadSubmissions(false)
    }

    function appendSubmissions(existing, batch) {
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

    // A submission's state is two facts: review status and media status.
    function stateOf(video) {
        var status = String(video.status || "")
        var media = String(video.media_status || "")
        if (status === "rejected")
            return { text: qsTr("未通过"), kind: "attention" }
        if (status === "pending")
            return { text: qsTr("待审核"), kind: "attention" }
        if (media === "failed")
            return { text: qsTr("转码失败"), kind: "attention" }
        if (media === "queued" || media === "processing")
            return { text: qsTr("转码中"), kind: "background" }
        return { text: qsTr("已发布"), kind: "live" }
    }

    Component.onCompleted: {
        loadSubmissions(true)
        if (demoMode === "publish-dialog")
            askDelete({ id: "", title: qsTr("第一次做手冲，翻车了"), status: "approved" })
    }

    // Deleting a submission is irreversible, so it always goes through the
    // board dialog; a published video says what else disappears with it.
    function askDelete(video) {
        root.pendingDelete = String(video.id || "")
        var state = stateOf(video)
        var published = state.kind === "live"
        dialog.ask({
            eyebrow: "DELETE",
            question: qsTr("删除《%1》？").arg(Http.firstOf(video.title, qsTr("未命名"))),
            detail: published
                ? qsTr("删掉之后，它会从推荐、搜索和你的主页里一起消失，无法恢复。")
                : qsTr("删掉之后这条投稿不会再出现在任何列表里，无法恢复。"),
            confirmLabel: qsTr("删除"),
            danger: true
        })
    }

    function deleteSubmission(id) {
        if (!id)
            return
        Api.request("publish.delete:" + id, "DELETE", "/v1/videos/" + id)
    }

    // Only a failed media job can be re-queued; the server answers 409 otherwise.
    function retryMedia(video) {
        if (!video || !video.id)
            return
        root.retrying = String(video.id)
        Api.request("publish.retry:" + video.id, "POST", "/v1/videos/" + video.id + "/media-retry", {})
    }

    ConfirmDialog {
        id: dialog
        onConfirmed: root.deleteSubmission(root.pendingDelete)
    }

    Connections {
        target: Api
        function onSessionChanged() {
            root.loadSubmissions()
        }
    }
    property string errorText: ""
    property string successText: ""

    function pick() {
        picker.open()
    }

    function startUpload() {
        if (filePath === "") {
            errorText = qsTr("先选一个视频文件。")
            return
        }
        uploading = true
        progress = 0
        errorText = ""
        successText = ""
        uploadId = ""
        publishKey = "desktop-" + Date.now() + "-" + Math.floor(Math.random() * 1000000)
        Api.upload("publish.upload", filePath, "")
    }

    function publish() {
        var list = tags.split(/[,，\s]+/)
        var clean = []
        for (var i = 0; i < list.length; i++) {
            if (list[i] !== "")
                clean.push(list[i])
        }
        if (root.uploadId === "") {
            root.errorText = qsTr("上传还没有完成，稍等一下再发。")
            return
        }
        // The gateway only accepts publishing a registered upload; a bare
        // video_url is rejected as unowned. The key makes a retry idempotent.
        Api.requestWithHeaders("publish.create", "POST", "/v1/videos", {
            title: title,
            description: description,
            tags: clean,
            upload_id: root.uploadId
        }, { "Idempotency-Key": root.publishKey })
    }

    FileDialog {
        id: picker
        title: qsTr("选一个视频")
        nameFilters: [qsTr("视频文件 (*.mp4 *.mov *.m4v *.webm *.mkv)")]
        onAccepted: {
            var path = selectedFile.toString()
            if (path.indexOf("file:///") === 0)
                path = decodeURIComponent(path.substring(8))
            root.filePath = path
        }
    }

    Connections {
        target: Api

        function onUploadProgress(tag, value) {
            if (tag === "publish.upload")
                root.progress = value
        }

        function onResult(tag, data) {
            var payload = data && data.data ? data.data : data

            if (tag === "publish.upload") {
                root.uploading = false
                if (payload && payload.upload_id) {
                    root.progress = 1
                    root.uploadId = payload.upload_id
                    root.publish()
                } else {
                    root.errorText = qsTr("服务端没有返回上传编号。")
                }
            } else if (tag === "publish.create") {
                var id = payload && (payload.video_id || payload.id)
                root.successText = id ? qsTr("已发布，编号 ") + id : qsTr("已发布。")
                root.title = ""
                root.description = ""
                root.tags = ""
                root.filePath = ""
                root.progress = 0
                if (root.router)
                    root.router.libraryChanged()
                root.loadSubmissions()
            } else if (tag.indexOf("publish.mine:") === 0) {
                var token = Http.num(tag.substring("publish.mine:".length))
                if (token !== root.submissionToken)
                    return // superseded by a newer load
                root.loadingSubmissions = false
                var payload = data && data.data ? data.data : data
                // The endpoint is author-scoped, cursor paged, and includes
                // pending rows.
                var mine = Http.items(data)
                root.submissions = root.submissionResetRequest
                    ? mine
                    : root.appendSubmissions(root.submissions, mine)
                root.submissionTotal = payload && payload.total !== undefined ? Http.num(payload.total) : mine.length
                root.hasMoreSubmissions = !!(payload && payload.has_more)
                root.submissionCursor = payload && payload.next_before ? String(payload.next_before) : ""
                if (root.scrollToSubmissions) {
                    root.scrollToSubmissions = false
                    pageScroll.contentY = Math.max(0, submissionsHeader.y - 12)
                }
            } else if (tag.indexOf("publish.delete:") === 0) {
                root.successText = qsTr("已删除这条投稿。")
                if (root.router && root.pendingDelete !== "")
                    root.router.videoDeleted(root.pendingDelete)
                root.pendingDelete = ""
                if (root.router)
                    root.router.libraryChanged()
                root.loadSubmissions()
            } else if (tag.indexOf("publish.retry:") === 0) {
                root.retrying = ""
                root.errorText = ""
                root.successText = qsTr("已重新排队，转码完成后状态会自己更新。")
                if (root.router)
                    root.router.libraryChanged()
                root.loadSubmissions()
            }
        }

        function onFailed(tag, status, code, message) {
            if (tag === "publish.upload" || tag === "publish.create") {
                root.uploading = false
                root.errorText = message
            } else if (tag.indexOf("publish.delete:") === 0) {
                root.errorText = status === 403 ? qsTr("只能删除自己的投稿。") : message
            } else if (tag.indexOf("publish.retry:") === 0) {
                root.retrying = ""
                if (status === 409)
                    root.errorText = qsTr("这条投稿的转码状态变了，刷新一下再看。")
                else if (status === 403)
                    root.errorText = qsTr("只有作者能重试自己的转码。")
                else
                    root.errorText = message
            }
        }
    }

    Flickable {
        id: pageScroll
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        contentWidth: width
        contentHeight: column.height
        clip: true

        Column {
            id: column
            width: Math.min(parent.width, 720)
            spacing: 0

            PairTitle { en: "PUBLISH"; cn: "发布" }

            Item { height: 10; width: 1 }

            FieldRow {
                fields: [
                    Api.authenticated ? Api.userName : qsTr("未登录"),
                    root.filePath !== "" ? qsTr("已选文件") : qsTr("未选文件")
                ]
            }

            Item { height: 16; width: 1 }
            TriStrip { width: 160 }
            Item { height: 20; width: 1 }

            // File picker block
            CutBlock {
                width: parent.width
                height: 96
                cut: Theme.cutButton
                corners: Qt.BottomRightCorner
                fill: Theme.chip

                Row {
                    anchors.centerIn: parent
                    spacing: 12

                    Icon { name: "upload"; size: 18; anchors.verticalCenter: parent.verticalCenter }
                    Text {
                        anchors.verticalCenter: parent.verticalCenter
                        text: root.filePath !== "" ? root.filePath.split(/[\\/]/).pop() : qsTr("选一个视频文件（mp4 / mov / webm）")
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 13
                        renderType: Text.NativeRendering
                    }
                }

                MouseArea {
                    anchors.fill: parent
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.pick()
                }
            }

            Item { height: 20; width: 1 }

            Text {
                text: qsTr("标题")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                font.weight: Font.DemiBold
                renderType: Text.NativeRendering
            }

            Item { height: 6; width: 1 }

            Rectangle {
                width: parent.width
                height: 44
                color: Theme.panel
                border.color: titleField.activeFocus ? Theme.trace : Theme.rule
                border.width: titleField.activeFocus ? 2 : 1

                TextInput {
                    id: titleField
                    anchors.fill: parent
                    anchors.leftMargin: 12
                    anchors.rightMargin: 12
                    verticalAlignment: TextInput.AlignVCenter
                    text: root.title
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 15
                    selectByMouse: true
                    selectionColor: Theme.signal
                    selectedTextColor: Theme.onSignal
                    onTextChanged: root.title = text
                }
            }

            Item { height: 16; width: 1 }

            Text {
                text: qsTr("说明")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                font.weight: Font.DemiBold
                renderType: Text.NativeRendering
            }

            Item { height: 6; width: 1 }

            Rectangle {
                width: parent.width
                height: 96
                color: Theme.panel
                border.color: descField.activeFocus ? Theme.trace : Theme.rule
                border.width: descField.activeFocus ? 2 : 1

                TextInput {
                    id: descField
                    anchors.fill: parent
                    anchors.margins: 12
                    text: root.description
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 14
                    wrapMode: TextInput.Wrap
                    selectByMouse: true
                    selectionColor: Theme.signal
                    selectedTextColor: Theme.onSignal
                    onTextChanged: root.description = text
                }
            }

            Item { height: 16; width: 1 }

            Text {
                text: qsTr("话题（用空格分隔）")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                font.weight: Font.DemiBold
                renderType: Text.NativeRendering
            }

            Item { height: 6; width: 1 }

            Rectangle {
                width: parent.width
                height: 44
                color: Theme.panel
                border.color: tagField.activeFocus ? Theme.trace : Theme.rule
                border.width: tagField.activeFocus ? 2 : 1

                TextInput {
                    id: tagField
                    anchors.fill: parent
                    anchors.leftMargin: 12
                    anchors.rightMargin: 12
                    verticalAlignment: TextInput.AlignVCenter
                    text: root.tags
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 14
                    selectByMouse: true
                    selectionColor: Theme.signal
                    selectedTextColor: Theme.onSignal
                    onTextChanged: root.tags = text
                }
            }

            Item { height: 20; width: 1 }

            Notice {
                width: parent.width
                visible: root.errorText !== ""
                text: root.errorText
            }

            Item { height: root.errorText !== "" ? 12 : 0; width: 1 }

            Notice {
                width: parent.width
                visible: root.successText !== ""
                text: root.successText
            }

            Item { height: root.successText !== "" ? 12 : 0; width: 1 }

            SegProgress {
                width: parent.width
                value: root.progress
                visible: root.uploading || root.progress > 0
            }

            Item { height: (root.uploading || root.progress > 0) ? 8 : 0; width: 1 }

            Text {
                visible: root.uploading || root.progress > 0
                text: "UPLOAD " + Math.round(root.progress * 100) + "%"
                color: Theme.ink2
                font.family: Theme.fontWide
                font.pixelSize: 11
                font.weight: Font.DemiBold
                font.letterSpacing: 11 * 0.14
                renderType: Text.NativeRendering
            }

            Item { height: 16; width: 1 }

            Row {
                spacing: 10

                ActionButton {
                    label: root.uploading ? qsTr("上传中…") : qsTr("上传并发布")
                    variant: "primary"
                    cornerColor: Theme.paper
                    enabled: !root.uploading
                    onClicked: root.startUpload()
                }

                ActionButton {
                    label: qsTr("重选文件")
                    variant: "ghost"
                    cornerColor: Theme.paper
                    onClicked: root.pick()
                }
            }

            Item { height: 28; width: 1 }

            Rectangle { width: parent.width; height: 1; color: Theme.rule }

            Item { height: 16; width: 1 }

            Row {
                id: submissionsHeader
                width: parent.width

                Column {
                    spacing: 2
                    width: parent.width - refreshSubmissions.width - toggleAll.width - 32

                    Eyebrow { label: "SUBMISSIONS" }

                    Text {
                        text: qsTr("我最近的投稿")
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 15
                        font.weight: Font.Bold
                        renderType: Text.NativeRendering
                    }
                }

                Text {
                    id: toggleAll
                    anchors.verticalCenter: parent.verticalCenter
                    text: root.showAll ? qsTr("收起") : qsTr("查看全部")
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 12
                    font.underline: true
                    renderType: Text.NativeRendering

                    MouseArea {
                        anchors.fill: parent
                        anchors.margins: -6
                        cursorShape: Qt.PointingHandCursor
                        onClicked: {
                            root.showAll = !root.showAll
                            // Only scroll when the reader asked for this, never on
                            // the initial load of the page.
                            root.scrollToSubmissions = true
                            root.loadSubmissions(true)
                        }
                    }
                }

                Item { width: 12; height: 1 }

                ActionButton {
                    id: refreshSubmissions
                    anchors.verticalCenter: parent.verticalCenter
                    label: qsTr("刷新")
                    icon: "refresh"
                    variant: "ghost"
                    cornerColor: Theme.paper
                    onClicked: root.loadSubmissions(true)
                }
            }

            Item { height: 10; width: 1 }

            Text {
                visible: !Api.authenticated
                width: parent.width
                text: qsTr("登录之后，这里会列出你投过的视频和它们的审核状态。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                wrapMode: Text.WordWrap
                renderType: Text.NativeRendering
            }

            Column {
                width: parent.width
                visible: Api.authenticated

                Repeater {
                    // Only the five newest unless the reader asked for the list.
                    model: root.showAll ? root.submissions : root.submissions.slice(0, 5)

                    delegate: Item {
                        id: submissionRow
                        required property var modelData
                        required property int index

                        width: parent.width
                        height: 46

                        readonly property var state: root.stateOf(modelData)

                        Row {
                            anchors.fill: parent
                            spacing: 12

                            Text {
                                width: Math.max(120, parent.width - 300)
                                anchors.verticalCenter: parent.verticalCenter
                                text: Http.firstOf(submissionRow.modelData.title, qsTr("未命名"))
                                color: Theme.ink
                                font.family: Theme.fontText
                                font.pixelSize: 13
                                elide: Text.ElideRight
                                renderType: Text.NativeRendering
                            }

                            StatusTag {
                                anchors.verticalCenter: parent.verticalCenter
                                label: submissionRow.state.text
                                kind: submissionRow.state.kind
                            }

                            FieldRow {
                                anchors.verticalCenter: parent.verticalCenter
                                fields: [
                                    Http.date(submissionRow.modelData.created_at),
                                    Http.count(Http.plays(submissionRow.modelData)) + qsTr(" 次播放")
                                ]
                                size: 11
                            }

                            Item {
                                visible: String(submissionRow.modelData.media_status || "") === "failed"
                                width: retryLabel.width
                                height: retryLabel.height
                                anchors.verticalCenter: parent.verticalCenter

                                Text {
                                    id: retryLabel
                                    text: root.retrying === String(submissionRow.modelData.id)
                                        ? qsTr("排队中…") : qsTr("重试转码")
                                    color: Theme.ink
                                    font.family: Theme.fontText
                                    font.pixelSize: 11
                                    font.underline: true
                                    renderType: Text.NativeRendering
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    anchors.margins: -6
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.retryMedia(submissionRow.modelData)
                                }
                            }

                            Item {
                                width: deleteRowLabel.width
                                height: deleteRowLabel.height
                                anchors.verticalCenter: parent.verticalCenter

                                Text {
                                    id: deleteRowLabel
                                    text: qsTr("删除")
                                    color: Theme.danger
                                    font.family: Theme.fontText
                                    font.pixelSize: 11
                                    renderType: Text.NativeRendering
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    anchors.margins: -6
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.askDelete(submissionRow.modelData)
                                }
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
                    visible: Api.authenticated && root.submissions.length === 0
                    text: root.loadingSubmissions ? qsTr("正在读取…") : qsTr("还没有投稿。上面选好文件就能发第一条。")
                    color: Theme.ink2
                    font.family: Theme.fontText
                    font.pixelSize: 12
                    renderType: Text.NativeRendering
                }

                ActionButton {
                    visible: root.showAll && root.hasMoreSubmissions
                    label: root.loadingSubmissions ? qsTr("读取中…") : qsTr("加载更多")
                    variant: "ghost"
                    cornerColor: Theme.paper
                    enabled: !root.loadingSubmissions
                    onClicked: root.loadMoreSubmissions()
                }
            }

            Item { height: 40; width: 1 }
        }
    }
}
