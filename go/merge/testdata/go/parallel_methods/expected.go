package calc

type Calc struct{}

func (c *Calc) Add(a, b int) int {
	return a + b
}

func (c *Calc) Sub(a, b int) int {
	return a - b
}

func (c *Calc) Mul(a, b int) int {
	return a * b
}
