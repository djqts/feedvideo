import QtQuick
import QtQuick.Shapes
import FeedVideo.Core 1.0

// The one pressable block: a corner cut off, and the accent wipes in from the
// left on hover/press. Primary buttons rest as ink on paper (on touch they rest
// as accent); secondary rest as a quiet chip; danger is text-only in red.
Item {
    id: root

    property string label: ""
    property string icon: ""
    property string variant: "primary"   // primary | secondary | ghost | danger
    property bool active: false
    property color cornerColor: Theme.paper
    property real cut: Theme.cutButton

    signal clicked()

    implicitHeight: 38 * Theme.density
    implicitWidth: content.implicitWidth + 30 * Theme.density + cut
    opacity: enabled ? 1 : 0.45

    readonly property color baseFill: {
        if (variant === "primary")
            return active ? Theme.signal : Theme.ink
        if (variant === "secondary")
            return active ? Theme.signal : Theme.chip
        return "transparent"
    }

    readonly property color baseText: {
        if (variant === "primary")
            return active ? Theme.onSignal : Theme.panel
        if (variant === "secondary")
            return active ? Theme.onSignal : Theme.ink
        if (variant === "danger")
            return Theme.danger
        return Theme.ink
    }

    CutBlock {
        anchors.fill: parent
        cut: root.cut
        corners: Qt.BottomRightCorner
        fill: root.baseFill
        strokeColor: root.variant === "ghost" ? Theme.rule : Theme.ink
        strokeWidth: (root.variant === "primary" || root.variant === "secondary") ? 0 : 1
    }

    // The wipe. Clipped by the button bounds; the cut corner is restored on top.
    Rectangle {
        id: wipe
        height: parent.height
        width: 0
        color: Theme.signal
        clip: true
    }

    Shape {
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        width: root.cut
        height: root.cut
        preferredRendererType: Shape.CurveRenderer
        ShapePath {
            strokeWidth: 0
            fillColor: root.cornerColor
            PathSvg {
                path: "M0 " + root.cut + " L" + root.cut + " " + root.cut + " L" + root.cut + " 0 Z"
            }
        }
    }

    Row {
        id: content
        anchors.centerIn: parent
        spacing: 8 * Theme.density

        Icon {
            visible: root.icon !== ""
            name: root.icon
            size: 16 * Theme.density
            color: label.color
            anchors.verticalCenter: parent.verticalCenter
        }

        Text {
            id: label
            text: root.label
            color: root.baseText
            font.family: Theme.fontText
            font.pixelSize: 14 * Theme.density
            font.weight: Font.DemiBold
            renderType: Text.NativeRendering

            Behavior on color {
                ColorAnimation { duration: Theme.reduceMotion ? 0 : 120; easing: Theme.ease }
            }
        }
    }

    Rectangle {
        anchors.fill: parent
        anchors.margins: 2
        color: "transparent"
        border.color: Theme.trace
        border.width: 2
        visible: root.activeFocus
    }

    MouseArea {
        anchors.fill: parent
        enabled: root.enabled
        hoverEnabled: true
        cursorShape: Qt.PointingHandCursor
        onClicked: root.clicked()

        onEntered: wipeIn()
        onExited: wipeOut()
        onPressed: wipeIn()
        onReleased: if (!containsMouse) wipeOut()

        function wipeIn() {
            wipeAnimation.stop()
            wipeAnimation.to = root.width
            wipeAnimation.restart()
            label.color = Theme.onSignal
        }

        function wipeOut() {
            wipeAnimation.stop()
            wipeAnimation.to = 0
            wipeAnimation.restart()
            label.color = root.baseText
        }
    }

    NumberAnimation {
        id: wipeAnimation
        target: wipe
        property: "width"
        duration: Theme.reduceMotion ? 0 : Theme.dWipe
        easing: Theme.mech
    }
}
