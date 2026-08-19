// alias.go
package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
)

var adjectives = []string{
	"brave", "quiet", "swift", "gentle", "bright", "calm", "bold", "lucky", "sunny", "misty",
	"happy", "clever", "shy", "proud", "wild", "soft", "sharp", "warm", "cool", "fresh",
}

var fruits = []string{
	"apple", "banana", "mango", "papaya", "lychee", "durian", "pineapple", "grape", "melon", "guava",
	"kiwi", "peach", "plum", "cherry", "lemon", "lime", "coconut", "fig", "date", "berry",
}

// generateAlias: เทียบเท่า generate_alias() ใน bash
func generateAlias() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	fruit := fruits[rand.Intn(len(fruits))]
	return fmt.Sprintf("%s-%s", adj, fruit)
}

// generateUniqueAlias: เทียบเท่า generate_unique_alias() ใน bash
func generateUniqueAlias(baseDir string) string {
	const maxAttempts = 100

	var alias string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		alias = generateAlias()
		path := filepath.Join(baseDir, alias)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return alias
		}
	}

	// ชนครบ 100 ครั้ง -> ต่อท้ายด้วยเลขสุ่มกันชัวร์ (เทียบ "${alias}-${RANDOM}")
	return fmt.Sprintf("%s-%d", alias, rand.Intn(32768))
}
