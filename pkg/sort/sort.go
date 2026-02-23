package sort

// NaturalLess compares two strings with number-aware ordering.
// Runs of digits are compared numerically so that "foo2" < "foo10".
func NaturalLess(a, b string) bool {
	for {
		if a == b {
			return false
		}
		if a == "" {
			return true
		}
		if b == "" {
			return false
		}

		// Find the next chunk (all-digits or all-non-digits) in each string.
		aDigit := a[0] >= '0' && a[0] <= '9'
		bDigit := b[0] >= '0' && b[0] <= '9'

		if aDigit != bDigit {
			// One starts with a digit, the other doesn't - compare as bytes.
			return a[0] < b[0]
		}

		if !aDigit {
			// Both are non-digit runs - compare character by character.
			i := 0
			for i < len(a) && i < len(b) && !(a[i] >= '0' && a[i] <= '9') && !(b[i] >= '0' && b[i] <= '9') {
				if a[i] != b[i] {
					return a[i] < b[i]
				}
				i++
			}
			a = a[i:]
			b = b[i:]
			continue
		}

		// Both are digit runs - compare numerically.
		ai, aj := 0, 0
		for aj < len(a) && a[aj] >= '0' && a[aj] <= '9' {
			aj++
		}
		bi, bj := 0, 0
		for bj < len(b) && b[bj] >= '0' && b[bj] <= '9' {
			bj++
		}

		// Strip leading zeros for numeric comparison.
		aNum := a[ai:aj]
		bNum := b[bi:bj]
		for len(aNum) > 1 && aNum[0] == '0' {
			aNum = aNum[1:]
		}
		for len(bNum) > 1 && bNum[0] == '0' {
			bNum = bNum[1:]
		}

		if len(aNum) != len(bNum) {
			return len(aNum) < len(bNum)
		}
		if aNum != bNum {
			return aNum < bNum
		}

		a = a[aj:]
		b = b[bj:]
	}
}
