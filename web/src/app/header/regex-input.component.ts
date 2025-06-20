/**
 * Copyright 2024 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { Component, EventEmitter, Input, Output } from '@angular/core';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { RegexValidator } from './regex-validator';
import { MatFormFieldModule } from '@angular/material/form-field';
import { CommonModule } from '@angular/common';
import { MatInputModule } from '@angular/material/input';

@Component({
  selector: 'khi-header-regex-input',
  templateUrl: './regex-input.component.html',
  styleUrls: ['./regex-input.component.scss'],
  imports: [
    CommonModule,
    MatFormFieldModule,
    MatInputModule,
    ReactiveFormsModule,
  ],
})
export class RegexInputComponent {
  @Input()
  label = '';

  @Output()
  regexFilterChange: EventEmitter<string> = new EventEmitter();

  regexInput: FormControl = new FormControl('', [RegexValidator()]);

  regexFormErrorMessage(): string {
    return this.regexInput.errors!['regex'] as string;
  }

  onFilterChange() {
    if (!this.regexInput.valid) return;
    this.regexFilterChange.emit(this.regexInput.value);
  }
}
