package debmeta

import (
	"strconv"
	"strings"
	"unicode"
)

// CompareVersions compares Debian versions. It returns -1, 0, or 1.
func CompareVersions(left, right string) int {
	leftEpoch, leftUpstream, leftRevision := splitVersion(left)
	rightEpoch, rightUpstream, rightRevision := splitVersion(right)

	if cmp := comparePart(leftEpoch, rightEpoch); cmp != 0 {
		return cmp
	}
	if cmp := comparePart(leftUpstream, rightUpstream); cmp != 0 {
		return cmp
	}
	return comparePart(leftRevision, rightRevision)
}

func splitVersion(version string) (epoch, upstream, revision string) {
	if index := strings.LastIndex(version, "-"); index != -1 {
		revision = version[index+1:]
		version = version[:index]
	}
	if index := strings.Index(version, ":"); index != -1 {
		epoch = version[:index]
		version = version[index+1:]
	}
	upstream = version
	return epoch, upstream, revision
}

func comparePart(left, right string) int {
	leftIndex, rightIndex := 0, 0
	for leftIndex < len(left) || rightIndex < len(right) {
		leftNonDigitEnd := leftIndex
		for leftNonDigitEnd < len(left) && !unicode.IsDigit(rune(left[leftNonDigitEnd])) {
			leftNonDigitEnd++
		}
		rightNonDigitEnd := rightIndex
		for rightNonDigitEnd < len(right) && !unicode.IsDigit(rune(right[rightNonDigitEnd])) {
			rightNonDigitEnd++
		}

		if cmp := compareLexicographic(left[leftIndex:leftNonDigitEnd], right[rightIndex:rightNonDigitEnd]); cmp != 0 {
			return cmp
		}

		leftIndex = leftNonDigitEnd
		rightIndex = rightNonDigitEnd

		leftDigitEnd := leftIndex
		for leftDigitEnd < len(left) && unicode.IsDigit(rune(left[leftDigitEnd])) {
			leftDigitEnd++
		}
		rightDigitEnd := rightIndex
		for rightDigitEnd < len(right) && unicode.IsDigit(rune(right[rightDigitEnd])) {
			rightDigitEnd++
		}

		if cmp := compareNumber(left[leftIndex:leftDigitEnd], right[rightIndex:rightDigitEnd]); cmp != 0 {
			return cmp
		}

		leftIndex = leftDigitEnd
		rightIndex = rightDigitEnd
	}
	return 0
}

func compareLexicographic(left, right string) int {
	leftIndex, rightIndex := 0, 0
	for leftIndex < len(left) || rightIndex < len(right) {
		leftOrder := lexicalOrder(left, leftIndex)
		rightOrder := lexicalOrder(right, rightIndex)

		if leftOrder < rightOrder {
			return -1
		}
		if leftOrder > rightOrder {
			return 1
		}

		if leftIndex < len(left) {
			leftIndex++
		}
		if rightIndex < len(right) {
			rightIndex++
		}
	}
	return 0
}

func lexicalOrder(value string, index int) int {
	if index >= len(value) {
		return 0
	}
	ch := value[index]
	if ch == '~' {
		return -1
	}
	if unicode.IsLetter(rune(ch)) {
		return int(ch)
	}
	return int(ch) + 256
}

func compareNumber(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")

	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}

	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}

	leftNumber, leftErr := strconv.Atoi(left)
	rightNumber, rightErr := strconv.Atoi(right)
	if leftErr == nil && rightErr == nil {
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
		return 0
	}

	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
