package main

import (
	"fmt"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
)

func main() {

	logger := logrus.New()
	lo.ForEach([]string{"A", "B", "C"}, func(item string, _ int) {
		logger.Info(item)
	})

	f, err := readGoModFile("go.mod")
	if err != nil {
		logger.Fatal(err)
	}
	for _, m := range f.Require {
		logger.Info(m.Mod.Path, " ", m.Mod.Version, " ", m.Mod.String())
	}

	goSum, err := readGoSumFile("go.sum")
	if err != nil {
		logger.Fatal(err)
	}
	for k, v := range goSum {
		logger.Info(k, " ", v)
	}

	fmt.Println(ModInfo(f, goSum))
}
