package tui

func halfPageStep(visible int) int {
	step := visible / 2
	if step < 1 {
		return 1
	}
	return step
}

func fullPageStep(visible int) int {
	step := visible - 1
	if step < 1 {
		return 1
	}
	return step
}
