//go:build e2e

package e2e_test

import (
	"context"
	"fmt"

	"github.com/chromedp/chromedp"
)

// clickVisibleControl measures after scrolling and verifies the hit target before
// dispatching a native click. On mobile login pages, CDP content quads can point
// at a footer link instead of the intended button after scrolling.
func clickVisibleControl(selector string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var point struct{ X, Y float64 }
		script := fmt.Sprintf(`(() => {
            const control = document.querySelector(%q);
            if (!control || control.disabled) throw new Error('control unavailable');
            control.scrollIntoView({behavior: 'instant', block: 'center', inline: 'center'});
            const bounds = control.getBoundingClientRect();
            const x = bounds.x + bounds.width / 2, y = bounds.y + bounds.height / 2;
            const hit = document.elementFromPoint(x, y);
            if (!bounds.width || !bounds.height || !(hit === control || control.contains(hit))) {
                throw new Error('control midpoint is not reachable');
            }
            return {X: x, Y: y};
        })()`, selector)
		if err := chromedp.Evaluate(script, &point).Do(ctx); err != nil {
			return fmt.Errorf("locate click target %s: %w", selector, err)
		}
		return chromedp.MouseClickXY(point.X, point.Y).Do(ctx)
	})
}
