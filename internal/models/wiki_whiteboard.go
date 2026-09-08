package models

type WikiWhiteboardObject struct {
	ID, WhiteboardID, Type, Title, Body, Color string
	X, Y, Width, Height                        int
}

type WikiWhiteboardConnector struct {
	ID, WhiteboardID, FromObjectID, ToObjectID, Label, Style string
	FromX, FromY, ToX, ToY                                   int
}

type WikiWhiteboardData struct {
	Objects    []WikiWhiteboardObject
	Connectors []WikiWhiteboardConnector
}
