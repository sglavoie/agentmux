package session

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// Resort moves every managed Kitty tab into the order produced by
// SortRunning(mode). Tabs already in the correct relative position are
// skipped. Unmanaged tabs are left alone. Returns the first error
// encountered; partial progress is preserved.
func Resort(client kitty.KittyClient, mode string) error {
	running, err := List(client)
	if err != nil {
		return err
	}
	desired := SortRunning(running, mode)
	if len(desired) < 2 {
		return nil
	}

	desiredIDs := make([]int, len(desired))
	for i, s := range desired {
		desiredIDs[i] = s.TabID
	}

	maxMoves := 2 * len(desired)
	moves := 0

	for i := 0; i < len(desired); i++ {
		for {
			managed, err := managedTabIDsInOrder(client)
			if err != nil {
				return err
			}

			if i == 0 {
				if len(managed) == 0 || managed[0] == desiredIDs[0] {
					break
				}
				if moves >= maxMoves {
					return fmt.Errorf("session: Resort exceeded %d move budget", maxMoves)
				}
				if err := client.DetachTab(
					kitty.MatchTabByID(managed[0]),
					kitty.MatchTabByID(desiredIDs[0]),
				); err != nil {
					return err
				}
				moves++
				continue
			}

			prevIdx := indexOfInt(managed, desiredIDs[i-1])
			if prevIdx < 0 {
				return fmt.Errorf("session: Resort lost track of tab %d", desiredIDs[i-1])
			}
			if prevIdx+1 < len(managed) && managed[prevIdx+1] == desiredIDs[i] {
				break
			}
			if moves >= maxMoves {
				return fmt.Errorf("session: Resort exceeded %d move budget", maxMoves)
			}
			if err := client.DetachTab(
				kitty.MatchTabByID(desiredIDs[i]),
				kitty.MatchTabByID(desiredIDs[i-1]),
			); err != nil {
				return err
			}
			moves++
		}
	}
	return nil
}

// managedTabIDsInOrder returns the TabIDs of all managed sessions in their
// current physical order, as reported by the Kitty client.
func managedTabIDsInOrder(client kitty.KittyClient) ([]int, error) {
	running, err := List(client)
	if err != nil {
		return nil, err
	}
	ids := make([]int, len(running))
	for i, s := range running {
		ids[i] = s.TabID
	}
	return ids, nil
}

func indexOfInt(s []int, v int) int {
	for i, e := range s {
		if e == v {
			return i
		}
	}
	return -1
}
