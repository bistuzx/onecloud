// Copyright 2019 Yunion
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package influxdb

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestInfluxdbResponseParser(t *testing.T) {
	Convey("Influxdb response parser", t, func() {
		Convey("Response parser", func() {
			parser := &ResponseParser{}

			response := &Response{
				Results: []Result{
					{
						Series: []Row{
							{
								Name:    "cpu",
								Columns: []string{"time", "mean", "sum"},
								Tags:    map[string]string{"datacenter": "America"},
								Values: [][]interface{}{
									{json.Number("111"), json.Number("222"), json.Number("333")},
									{json.Number("111"), json.Number("222"), json.Number("333")},
									{json.Number("111"), json.Number("null"), json.Number("333")},
								},
							},
						},
					},
				},
			}

			query := &Query{}

			result := parser.Parse(response, query)

			Convey("can parse all series", func() {
				So(len(result.Series), ShouldEqual, 1)
			})

			Convey("can parse all points", func() {
				So(len(result.Series[0].Points), ShouldEqual, 3)
				//So(len(result.Series[1].Points), ShouldEqual, 3)
			})

			Convey("can parse multi row result", func() {
				So(result.Series[0].Points[1].Value(), ShouldEqual, float64(222))
				//So(result.Series[1].Points[1].Value(), ShouldEqual, float64(333))
			})

			Convey("can parse null points", func() {
				So(result.Series[0].Points[2].IsValid(), ShouldBeFalse)
			})

			Convey("can format serie names", func() {
				So(result.Series[0].Name, ShouldEqual, "cpu.mean-sum")
				So(result.Series[0].Tags, ShouldResemble, map[string]string{"datacenter": "America"})
				//So(result.Series[1].Name, ShouldEqual, "cpu.sum")
				//So(result.Series[1].Tags, ShouldResemble, map[string]string{"datacenter": "America"})
			})
		})

		Convey("Response parser with alias", func() {
			parser := &ResponseParser{}

			response := &Response{
				Results: []Result{
					{
						Series: []Row{
							{
								Name:    "cpu.upc",
								Columns: []string{"time", "mean", "sum"},
								Tags: map[string]string{
									"datacenter":     "America",
									"dc.region.name": "Northeast",
								},
								Values: [][]interface{}{
									{json.Number("111"), json.Number("222"), json.Number("333")},
								},
							},
						},
					},
				},
			}

			Convey("$ alias", func() {
				Convey("simple alias", func() {
					query := &Query{Alias: "serie alias"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "serie alias")
				})

				Convey("measurement alias", func() {
					query := &Query{Alias: "alias $m $measurement", Measurement: "10m"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias 10m 10m")
				})

				Convey("column alias", func() {
					query := &Query{Alias: "alias $col", Measurement: "10m"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias mean-sum")
					//So(result.Series[1].Name, ShouldEqual, "alias sum")
				})

				Convey("tag alias", func() {
					query := &Query{Alias: "alias $tag_datacenter"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias America")
				})

				Convey("segment alias", func() {
					query := &Query{Alias: "alias $1"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias upc")
				})

				Convey("segment position out of bound", func() {
					query := &Query{Alias: "alias $5"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias $5")
				})
			})

			Convey("[[]] alias", func() {
				Convey("simple alias", func() {
					query := &Query{Alias: "serie alias"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "serie alias")
				})

				Convey("measurement alias", func() {
					query := &Query{Alias: "alias [[m]] [[measurement]]", Measurement: "10m"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias 10m 10m")
				})

				Convey("column alias", func() {
					query := &Query{Alias: "alias [[col]]", Measurement: "10m"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias mean-sum")
					//So(result.Series[1].Name, ShouldEqual, "alias sum")
				})

				Convey("tag alias", func() {
					query := &Query{Alias: "alias [[tag_datacenter]]"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias America")
				})

				Convey("tag alias with periods", func() {
					query := &Query{Alias: "alias [[tag_dc.region.name]]"}
					result := parser.Parse(response, query)

					So(result.Series[0].Name, ShouldEqual, "alias Northeast")
				})
			})
		})
	})
}
